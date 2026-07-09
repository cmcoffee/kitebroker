package kiteworks

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	. "github.com/cmcoffee/kitebroker/core"
)

// unappliedFields returns the names of fields that were sent in `sent` but do
// not match the corresponding value in the profile the server saved — i.e.
// fields the API accepted (HTTP 200) but silently did not apply.
func unappliedFields(sent PostJSON, saved KWProfileFeatures) []string {
	savedMap, err := FeaturesToMap(saved)
	if err != nil {
		return nil
	}
	var ignored []string
	for k, want := range sent {
		got, ok := savedMap[k]
		if !ok || !reflect.DeepEqual(fmt.Sprintf("%v", got), fmt.Sprintf("%v", want)) {
			ignored = append(ignored, k)
		}
	}
	sort.Strings(ignored)
	return ignored
}

// CloneProfiles mirrors the source server's custom profiles onto the
// destination. Built-in profiles (Standard/Restricted/etc.) already exist on
// every appliance and are skipped; only custom profiles (BuiltIn == 0) are
// synced. Matching is by name, consistent with MapProfiles/FindProfile.
//
// Cloning is a two-step operation, matching the Kiteworks API:
//  1. POST /rest/profiles creates a named custom profile cloned from a built-in
//     prototype (this only establishes name + prototype);
//  2. PUT /rest/profiles/{id} applies the full feature configuration.
//
// For a profile that already exists on the destination by name, step 1 is
// skipped and only the feature configuration is pushed (create-and-update-to-
// match). The prototype is resolved by mapping the source profile's built-in
// prototype to the destination built-in of the same name.
func (T *KW_TO_KWTask) CloneProfiles() (err error) {
	Log("\n=== Cloning custom profiles to destination. ===\n\n")

	created := T.Report.Tally("Profiles Created")
	updated := T.Report.Tally("Profiles Updated")

	src_profiles, err := T.SRC.Session(T.src_admin).FullProfiles()
	if err != nil {
		return err
	}
	dst_profiles, err := T.KW.FullProfiles()
	if err != nil {
		return err
	}

	// Index destination profiles by lowercased name, and build a built-in
	// prototype map (built-in id by name) used to resolve the clone prototype.
	dst_by_name := make(map[string]KWFullProfile)
	dst_builtin_by_name := make(map[string]int)
	var dst_default_builtin int
	for _, p := range dst_profiles {
		dst_by_name[strings.ToLower(p.Name)] = p
		if p.BuiltIn != 0 {
			dst_builtin_by_name[strings.ToLower(p.Name)] = p.ID
			if dst_default_builtin == 0 {
				dst_default_builtin = p.ID
			}
		}
	}

	// Map source profile ids to destination built-in ids by name, so we can
	// resolve a source profile's prototype to a destination prototype.
	src_builtin_name := make(map[int]string)
	for _, p := range src_profiles {
		if p.BuiltIn != 0 {
			src_builtin_name[p.ID] = strings.ToLower(p.Name)
		}
	}

	for _, sp := range src_profiles {
		features := sp.Features

		dp, exists := dst_by_name[strings.ToLower(sp.Name)]
		if !exists {
			// Built-in profiles are present on every appliance and cannot be
			// created — if a same-named built-in isn't on the destination there's
			// nothing we can do, so skip. (Only custom profiles are created.)
			if sp.BuiltIn != 0 {
				Debug("Built-in profile '%s' not found on destination by name; skipping.", sp.Name)
				continue
			}
			prototype := T.resolvePrototype(sp.Prototype, src_builtin_name, dst_builtin_by_name, dst_default_builtin)
			if prototype == 0 {
				Err("Cannot clone profile '%s': no built-in prototype available on destination.", sp.Name)
				continue
			}
			Log("Creating profile '%s' (prototype id %d).", sp.Name, prototype)
			np, cerr := T.KW.NewProfile(sp.Name, prototype)
			if cerr != nil {
				if IsAPIError(cerr, "ERR_ENTITY_EXISTS") {
					// Race/stale cache: re-fetch below via name.
					Debug("Profile '%s' already exists, will update.", sp.Name)
					refreshed, rerr := T.KW.FullProfiles()
					if rerr == nil {
						for _, p := range refreshed {
							if strings.EqualFold(p.Name, sp.Name) {
								dp = p
								exists = true
								break
							}
						}
					}
				} else {
					Err("Failed to create profile '%s': %v", sp.Name, cerr)
					continue
				}
			} else {
				dp = np
				exists = true
				created.Add(1)
			}
		}

		if !exists {
			continue
		}

		// Send only the fields that differ from the destination's current
		// configuration. Resending fields that already match would trip built-in
		// profiles' conditional per-field validation (allowed_if / not_in_list)
		// on fields we don't even need to change — so diffing keeps each PUT to
		// the minimal set of genuine changes (e.g. just {"allowSftp": true}).
		diff, derr := FeaturesDiff(dp.Features, features)
		if derr != nil {
			Err("Failed to diff profile '%s': %v", sp.Name, derr)
			continue
		}
		if len(diff) == 0 {
			Debug("Profile '%s' (id %d) already matches source; nothing to update.", sp.Name, dp.ID)
			continue
		}
		if _, uerr := T.KW.UpdateProfileFields(dp.ID, diff); uerr != nil {
			Err("Failed to configure profile '%s' (id %d): %v", sp.Name, dp.ID, uerr)
			continue
		}
		// Verify by re-fetching the profile (the PUT returns an empty body on
		// this appliance, so we can't trust its echo). Any field we sent that
		// still differs was silently ignored — typically because it's gated by a
		// system-level setting (e.g. allowSftp when SFTP is disabled appliance-wide).
		if after, gerr := T.KW.GetProfile(dp.ID); gerr == nil {
			if ignored := unappliedFields(diff, after.Features); len(ignored) > 0 {
				Warn("Profile '%s' (id %d): server accepted but did not apply field(s): %s. Check system-level settings on the destination.", sp.Name, dp.ID, strings.Join(ignored, ", "))
			}
		}
		Log("Configured profile '%s' (id %d): updated %d field(s) to match source.", sp.Name, dp.ID, len(diff))
		updated.Add(1)
	}

	return nil
}

// resolvePrototype maps a source profile's prototype (a source built-in id) to
// the destination built-in id of the same name. Falls back to the destination's
// default built-in when the prototype is absent or has no name match.
func (T *KW_TO_KWTask) resolvePrototype(src_prototype *int, src_builtin_name map[int]string, dst_builtin_by_name map[string]int, dst_default int) int {
	if src_prototype != nil {
		if name, ok := src_builtin_name[*src_prototype]; ok {
			if id, ok := dst_builtin_by_name[name]; ok {
				return id
			}
		}
	}
	return dst_default
}
