/* Package 'cfg' provides functions for reading and writing configuration files and their coresponding string values.
   Ignores '#' as comments, ','s denote multiple values.

   # Example config file.
   [section]
   key = value
   key2 = value1, value2
   key3 = value1,
          value2,
          value3

   [section2]
   key = value1,
         value2,
         value3
*/
package cfg

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type Store struct {
	file     string
	mutex    sync.RWMutex
	cfgStore map[string]map[string][]string
}

const (
	cfg_HEADER = 1 << iota
	cfg_KEY
	cfg_COMMA
	cfg_ESCAPE
)

const empty = ""

// Returns entire line as one string, (Single Get)
func (s *Store) SGet(section, key string) string {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if s.cfgStore == nil {
		return empty
	}

	if result, found := s.cfgStore[section][key]; !found {
		return empty
	} else {
		if len(result) == 0 {
			return empty
		}
		return strings.Join(result, ", ")
	}
}

// Returns array of all retrieved string values under section with key.
func (s *Store) MGet(section, key string) []string {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if s.cfgStore == nil {
		return []string{}
	}

	if result, found := s.cfgStore[section][key]; !found {
		return []string{}
	} else {
		if len(result) == 0 {
			return []string{}
		}
		return result
	}
}

// Goes through list of sections and keys to make sure they are set.
func (s *Store) Sanitize(section string, keys []string) (err error) {
	if s.cfgStore == nil {
		return fmt.Errorf("[%s] section does not exist, or is not configured.", section)
	}
	if _, ok := s.cfgStore[section]; !ok {
		return fmt.Errorf("[%s] section does not exist, or is not configured.", section)
	}
	var missing_keys []string
	for _, key := range keys {
		if found := s.Exists(section, key); !found {
			missing_keys = append(missing_keys, fmt.Sprintf("'%s'", key))
		}
	}
	if len(missing_keys) > 0 {
		return fmt.Errorf("[%s] section lacks required keys: %s.", section, strings.Join(missing_keys, ", "))
	}
	return
}

// Return only the first entry, if there are multiple entries the rest are skipped.
func (s *Store) Get(section, key string) string {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if s.cfgStore == nil {
		return empty
	}

	var (
		result []string
		found  bool
	)

	if result, found = s.cfgStore[section][key]; !found {
		return empty
	}

	res_len := len(result)

	if res_len == 0 {
		return empty
	}

	return result[0]
}

// Get Boolean Value from config.
func (s *Store) GetBool(section, key string) (output bool) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if s.cfgStore == nil {
		return false
	}

	var (
		result []string
		found  bool
	)

	if result, found = s.cfgStore[section][key]; !found {
		return false
	}

	result_str := strings.ToLower(result[0])
	switch result_str {
	case "yes":
		return true
	case "true":
		return true
	default:
		return false
	}

	return
}

// Get Int64 Value from config.
func (s *Store) GetInt(section, key string) (output int64) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if s.cfgStore == nil {
		return 0
	}

	var (
		result []string
		found  bool
	)

	if result, found = s.cfgStore[section][key]; !found {
		return 0
	}

	output, _ = strconv.ParseInt(result[0], 10, 64)

	return
}

// Get UInt64 Value from config.
func (s *Store) GetUint(section, key string) (output uint64) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if s.cfgStore == nil {
		return 0
	}

	var (
		result []string
		found  bool
	)

	if result, found = s.cfgStore[section][key]; !found {
		return 0
	}

	output, _ = strconv.ParseUint(result[0], 10, 64)

	return
}

// Get Float64 Value from config.
func (s *Store) GetFloat(section, key string) (output float64) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if s.cfgStore == nil {
		return 0.0
	}

	var (
		result []string
		found  bool
	)

	if result, found = s.cfgStore[section][key]; !found {
		return 0.0
	}

	output, _ = strconv.ParseFloat(result[0], 64)

	return
}

// Returns array of all sections in config file.
func (s *Store) Sections() (out []string) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if s.cfgStore == nil {
		return []string{empty}
	}

	for section, _ := range s.cfgStore {
		out = append(out, section)
	}
	sort.Strings(out)
	return
}

// Returns keys of section specified.
func (s *Store) Keys(section string) (out []string) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if v, ok := s.cfgStore[section]; !ok {
		return []string{empty}
	} else {
		for key, _ := range v {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return
}

// Returns true if section or section and key exists.
func (s *Store) Exists(input ...string) (found bool) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if s.cfgStore == nil {
		return false
	}

	inlen := len(input)
	if inlen == 0 {
		return false
	}

	if inlen > 0 {
		if _, found = s.cfgStore[input[0]]; !found {
			return
		}
	}
	if inlen > 1 {
		if found == true {
			_, found = s.cfgStore[input[0]][input[1]]
			return
		}
	}
	return
}

// Unsets a specified key, or specified section.
// If section is empty, section is removed.
func (s *Store) Unset(input ...string) {

	if s.cfgStore == nil {
		return
	}

	switch len(input) {
	case 0:
		return
	case 1:
		keys := s.Keys(input[0])
		s.mutex.Lock()
		for _, key := range keys {
			delete(s.cfgStore[input[0]], key)
		}
	default:
		s.mutex.Lock()
		delete(s.cfgStore[input[0]], input[1])
	}
	s.mutex.Unlock()
}

// Sets key = values under [section], updates Store and saves to file.
func (s *Store) Set(section, key string, value ...interface{}) (err error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	var newValue []string

	if s.cfgStore == nil {
		s.cfgStore = make(map[string]map[string][]string)
	}

	for _, val := range value {
		newValue = append(newValue, fmt.Sprintf("%v", val))
	}

	// Create new map if one doesn't exist.
	if _, ok := s.cfgStore[section]; !ok {
		s.cfgStore[section] = make(map[string][]string)
	}

	if len(value) == 0 {
		delete(s.cfgStore[section], key)
	} else {
		s.cfgStore[section][key] = newValue
	}
	return
}

// Creates error output when config file has error.
func cfgErr(line int) error {
	return fmt.Errorf("Syntax error found on line %d.", line)
}

// Splits on rune
func cleanSplit(input string, sepr rune, instances int) (out []string) {
	var skip, quoted bool
	var last, q_start, q_end int

	q_start = -1
	q_end = -1
	i_len := len(input)

	//if instances > 0 {
	//	instances++
	//}

	for n, ch := range input {
		switch ch {
		case '"':
			if !quoted && !skip {
				quoted = true
				if i_len >= n+1 {
					q_start = n + 1
				}
			} else if quoted && !skip {
				quoted = false
				q_end = n
			}
		case '\\':
			skip = true
		case sepr:
			if !quoted {
				if skip {
					skip = false
				} else if instances != 0 {
					if q_start >= 0 {
						if q_end == -1 {
							q_end = n
						}
						out = append(out, input[q_start:q_end])
						q_start = -1
						q_end = -1
					} else {
						out = append(out, input[last:n])
					}
					last = n
					instances--
				}
			}
		default:
			skip = false
		}
	}

	if instances >= 0 {
		out = append(out, input[last:])
	}

	if instances < 0 {
		if q_start >= 0 && q_end >= 0 {
			out = append(out, input[q_start:q_end])
		} else {
			out = append(out, input[last:])
		}
	}

	for n, _ := range out {
		olen := len(out[n])
		if olen > 0 {
			if rune(out[n][0]) == sepr {
				out[n] = out[n][1:olen]
			}
		} else {
			out[n] = empty
		}
		out[n] = strings.TrimSpace(out[n])
	}
	return
}

// Parses the configuration data.
func (s *Store) config_parser(input io.Reader, overwrite bool) (err error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	sc := bufio.NewScanner(input)

	if s.cfgStore == nil {
		s.cfgStore = make(map[string]map[string][]string)
	}

	var section, key string
	var line int
	var added_sections []string
	var added_keys []string

	for sc.Scan() {
		line++
		txt := strings.TrimSpace(cleanSplit(sc.Text(), '#', 1)[0])

		write_ok := func(key string) bool {
			if overwrite {
				return true
			}
			for _, k := range added_keys {
				if k == key {
					return true
				}
			}
			return false
		}

		if len(txt) == 0 {
			continue
		}
		if txt[0] == '[' && txt[len(txt)-1] == ']' {
			added_keys = make([]string, 0)
			section = strings.TrimSuffix(strings.TrimPrefix(txt, "["), "]")
			for _, s := range added_sections {
				if s == section {
					return fmt.Errorf("Duplicate section [%s] encountered on line %d.", section, line)
				}
			}
			added_sections = append(added_sections, section)
			if s.cfgStore[section] == nil {
				s.cfgStore[section] = make(map[string][]string)
			}
		} else {
			if section == empty {
				return cfgErr(line)
			}
			split := cleanSplit(txt, '=', 1)
			if len(split) == 2 {
				key = strings.TrimSpace(split[0])
				txt = strings.TrimSpace(split[1])
				if _, ok := s.cfgStore[section][key]; !ok {
					added_keys = append(added_keys, key)
				}
				if write_ok(key) {
					delete(s.cfgStore[section], key)
				}
			}
			if write_ok(key) {
				for _, v := range cleanSplit(txt, ',', -1) {
					if len(v) > 0 {
						s.cfgStore[section][key] = append(s.cfgStore[section][key], strings.TrimSpace(v))
					}
				}
			}

		}
	}
	return nil
}

// Sets default settings for configuration store, ignores if already set.
func (s *Store) Defaults(input string) (err error) {
	return s.config_parser(strings.NewReader(input), false)
}

// Will parse a string, but overwrite existing config.
func (s *Store) Parse(input string) (err error) {
	return s.config_parser(strings.NewReader(input), true)
}

// Reads configuration file and returns Store, file must exist even if empty.
func (s *Store) File(file string) (err error) {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	err = s.config_parser(f, true)
	if err != nil {
		return fmt.Errorf("%s: %s", file, err)
	}
	s.file = file
	return
}

// TrimSave is similar to Save, however it will trim unusued keys.
func (s *Store) TrimSave(sections ...string) error {
	return s.save(true, sections...)
}

// Saves [section](s) to file, recording all key = value pairs, if empty, save all sections.
func (s *Store) Save(sections ...string) error {
	return s.save(false, sections...)
}

func (s *Store) save(clear_unused_keys bool, sections ...string) error {

	if s.file == empty {
		return fmt.Errorf("No file specified for write operation.")
	}

	if len(sections) == 0 {
		sections = s.Sections()
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	f, err := os.Open(s.file)
	if err != nil {
		if os.IsNotExist(err) {
			f, err = os.Create(s.file)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}

	// interface for copying file content to ram and back to disk.
	type source interface {
		Seek(offset int64, whence int) (int64, error)
		Read(p []byte) (n int, err error)
	}

	// Copys line start to line end of src file to dst file.
	copyFile := func(src source, dst io.Writer, start, end int) error {
		_, err := src.Seek(0, 0)
		if err != nil {
			return err
		}

		s := bufio.NewScanner(src)
		var line int

		for line < start {
			s.Scan()
			line++
		}

		for (line < end || end == -1) && s.Scan() {
			line++
			_, err := io.WriteString(dst, s.Text()+"\n")
			if err != nil {
				return err
			}
		}
		return nil
	}

	// cfgSeek returns first half and bottom half of file, excluding the key = value.
	cfgSeek := func(section string, f source) (upper int, lower int) {
		f.Seek(0, 0)
		s := bufio.NewScanner(f)

		var line int

		upper = -1

		for s.Scan() {
			line++
			b := strings.TrimSpace(s.Text())
			l := len(b)

			if l > 0 && b[0] == '#' || l == 0 {
				continue
			}

			// Record the begining of the next section
			if strings.HasPrefix(b, "[") {
				if strings.HasPrefix(b, "["+section+"]") {
					upper = line - 1
					continue
				} else if upper > -1 {
					lower = line - 1
					return
				}
			}
		}
		if upper == -1 {
			upper = line
		}
		return upper, line
	}

	// Stores Key Value pairs
	storeKV := func(dst *bytes.Buffer, k string, keymap map[string][]string) (err error) {
		v := keymap[k]
		if len(v) == 0 && clear_unused_keys {
			return nil
		}
		_, err = dst.WriteString(k + " = ")
		if err != nil {
			return err
		}
		spacer := make([]byte, len(k+" = "))
		for n, _ := range spacer {
			spacer[n] = ' '
		}
		vlen := len(v)
		var str string
		if vlen == 0 {
			_, err = dst.WriteString(str + "\n")
			return
		}
		for n, txt := range v {
			if strings.Contains(txt, ",") {
				txt = strconv.Quote(txt)
			}
			if n > 0 {
				str = fmt.Sprintf("%s%s", spacer, txt)
			} else {
				str = txt
			}
			if n == vlen-1 {
				_, err = dst.WriteString(str + "\n")
			} else {
				_, err = dst.WriteString(str + ",\n")
			}
			if err != nil {
				return err
			}
		}
		return
	}

	tmp_dst := new(bytes.Buffer)

	// Copy entire config into memory.
	err = copyFile(f, tmp_dst, 0, -1)
	if err != nil {
		return err
	}
	f.Close()

	var src_buf []byte

	for _, section := range sections {
		wb_sz := tmp_dst.Len()
		rd_sz := cap(src_buf)

		if rd_sz < wb_sz {
			src_buf = append(src_buf[:rd_sz], make([]byte, wb_sz-rd_sz)[0:]...)
		}

		src_buf = src_buf[0:wb_sz]

		copy(src_buf, tmp_dst.Bytes())
		tmp_src := bytes.NewReader(src_buf)

		tmp_dst.Reset()

		head, tail := cfgSeek(section, tmp_src)

		err = copyFile(tmp_src, tmp_dst, 0, head)
		if err != nil {
			return err
		}

		if _, ok := s.cfgStore[section]; ok {

			var sec_buf bytes.Buffer

			err = copyFile(tmp_src, &sec_buf, head, tail)
			if err != nil {
				return err
			}

			var used_keys []string

			if _, err = tmp_dst.WriteString("[" + section + "]\n"); err != nil {
				return err
			}

			sc := bufio.NewScanner(&sec_buf)
			for sc.Scan() {
				raw := sc.Text()
				txt := strings.TrimSpace(raw)
				if len(txt) == 0 {
					_, err = tmp_dst.WriteString("\n")
					if err != nil {
						return err
					}
					continue
				}
				switch txt[0] {
				case '#':
					_, err = tmp_dst.WriteString(raw + "\n")
					if err != nil {
						return err
					}
				case '[':
					if txt[len(txt)-1] == ']' {
						if txt == "["+section+"]" {
							continue
						}
					}
				default:
					if strings.ContainsRune(txt, '=') {
						key := strings.TrimSpace(strings.Split(txt, "=")[0])
						if err = storeKV(tmp_dst, key, s.cfgStore[section]); err != nil {
							return err
						}
						used_keys = append(used_keys, key)
					}
				}
			}

			var all_keys []string

			for key, _ := range s.cfgStore[section] {
				all_keys = append(all_keys, key)
			}
			sort.Strings(all_keys)

		outter_loop:
			for _, k := range all_keys {
				for _, key := range used_keys {
					if k == key {
						continue outter_loop
					}
				}
				if err = storeKV(tmp_dst, k, s.cfgStore[section]); err != nil {
					return err
				}
			}
			//if _, err = tmp_dst.WriteString("\n"); err != nil { return err }
		}
		if err = copyFile(tmp_src, tmp_dst, tail, -1); err != nil {
			return err
		}
	}

	destfile, err := os.OpenFile(s.file, os.O_RDWR|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer destfile.Close()

	_, err = io.Copy(destfile, tmp_dst)
	if err != nil {
		return err
	}

	err = destfile.Sync()
	if err != nil {
		return err
	}

	return nil
}
