package core

import (
	"crypto/tls"
	"fmt"
	"github.com/cmcoffee/snugforge/iotimeout"
	"github.com/cmcoffee/snugforge/nfo"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// kitebrokerUpdater manages the update process for Kitebroker.
// It encapsulates the necessary information and methods to
// check for, download, and apply updates.
type kitebrokerUpdater struct {
	updateServer string
	appName      string
	localVer     string
	remoteVer    string
	localExec    string
	localPath    string
	sslVerify    bool
	proxyURL     string
}

//global.cfg.Get("configuration", "proxy_uri")
//!global.cfg.GetBool("configuration", "ssl_verify")

// UpdateKitebroker Updates the kitebroker.ioapp.(*http.io. ReaderFrom io.Reader{io.NewSection(io.NewSectionReader(io.NewBuffer(buffer.(*Bytes").Bytes()io.NewReader(buffer.(*buffer).Bytes())io.NewBuffer(buffer.(*buffer).Bytes())})})
// with the latest version if available.
func UpdateKitebroker(appName string, localVer string, localPath string, localExec string, sslVerify bool, proxyURL string) {

	k := &kitebrokerUpdater{
		appName:   appName,
		localVer:  localVer,
		localPath: localPath,
		localExec: localExec,
		sslVerify: sslVerify,
		proxyURL:  proxyURL,
	}

	k.updateServer = "dist.snuglab.com"

	update_avail := false

	update_avail, k.remoteVer = k.check_for_update()
	Log("Local Version:\t%s", k.localVer)
	Log("Remote Version:\t%s\n\n", k.remoteVer)
	if update_avail {
		if val := nfo.ConfirmDefault("Update Available! Download?", true); val {
			k.update_self()
			return
		}
	} else {
		Log("No update available.")
	}
}

// check_for_update checks for a newer versions of the application.
// It returns true if an update is available, along with the remote version.
// Otherwise, it returns false and the remote version.
func (k kitebrokerUpdater) check_for_update() (bool, string) {
	//Log("### %s online-update ###\n\n", k.appName)
	Log("Checking with https://%s...\n\n", k.updateServer)
	resp, err := k.http_get(fmt.Sprintf("https://%s/kitebroker/version.txt", k.updateServer))
	if err != nil {
		Fatal(err)
	}
	defer resp.Body.Close()
	remote_ver, err := io.ReadAll(resp.Body)
	if err != nil {
		Fatal(err)
	}

	pad := func(num int) string {
		if num < 10 {
			return fmt.Sprintf("0%d", num)
		}
		return fmt.Sprintf("%d", num)
	}

	cleanup_version := func(input string) (int64, error) {
		vers := strings.Split(input, ".")

		var ns []string
		for _, n := range vers {
			for _, v := range strings.Split(n, "-") {
				val, err := strconv.Atoi(v)
				if err != nil {
					return 0, err
				}
				ns = append(ns, fmt.Sprintf("%s", pad(val)))
			}
		}
		num, _ := strconv.ParseInt(strings.Join(ns, ""), 10, 64)
		return num, nil
	}

	var (
		r_ver int64
		l_ver int64
	)

	r_ver, err = cleanup_version(string(remote_ver))
	if err != nil {
		Fatal("Could not determine remote version: %s", remote_ver)
	}
	l_ver, err = cleanup_version(k.localVer)
	if err != nil {
		Fatal("Could not determine local version: %s", remote_ver)
	}

	Debug("Remote: %d vs Local: %d", r_ver, l_ver)

	if r_ver > l_ver {
		return true, string(remote_ver)
	}
	return false, string(remote_ver)
}

// update_self attempts to download and replace the current executable.
func (k kitebrokerUpdater) update_self() {
	defer Exit(0)

	current_os := runtime.GOOS

	build := fmt.Sprintf("%s-%s", current_os, runtime.GOARCH)

	resp, err := k.http_get(fmt.Sprintf("https://%s/kitebroker/%s/%s", k.updateServer, build, k.localExec))
	if err != nil {
		Fatal(err)
	}
	defer resp.Body.Close()

	temp_file_name := LocalPath(fmt.Sprintf("%s/%s.incomplete", k.localPath, fmt.Sprintf("%s.%s", k.localExec, build)))

	os.Remove(temp_file_name)

	f, err := os.OpenFile(temp_file_name, os.O_CREATE|os.O_RDWR, 0775)
	Critical(err)

	Defer(func() { os.Remove(temp_file_name) })

	// If the filesize is different, online version is different.
	Log("\nDownloading latest %s update from %s...", k.appName, k.updateServer)
	src := transferMonitor("Download Update", resp.ContentLength, rightToLeft|nfo.ProgressBarSummary, nopSeeker(iotimeout.NewReadCloser(resp.Body, time.Minute)))

	_, err = io.Copy(f, src)
	if err != nil {
		Critical(err)
	}

	f.Close()
	resp.Body.Close()
	src.Close()

	var (
		final_msg string
		dest_file string
	)

	dest_file = LocalPath(fmt.Sprintf("%s/%s", k.localPath, k.localExec))

	if current_os == "windows" {
		file_split := strings.Split(k.localExec, ".")
		if err = os.Rename(dest_file, fmt.Sprintf("%s/%s.old", k.localPath, file_split[0])); err != nil {
			Fatal(err)
		}
	}

	final_msg = fmt.Sprintf("\n%s has been updated to the latest version: %s", k.appName, k.remoteVer)

	if err = os.Rename(temp_file_name, dest_file); err != nil {
		Fatal(err)
	}

	Log(final_msg)
	return
}

// http_get fetches the content from the given URL using HTTP GET.
// It handles proxy settings and SSL verification.
func (k kitebrokerUpdater) http_get(target string) (*http.Response, error) {
	var (
		transport http.Transport
		proxy_url *url.URL
		err       error
	)

	if !IsBlank(k.proxyURL) {
		proxy_url, err = url.Parse(strings.Join([]string{k.proxyURL}, ""))
		if err != nil {
			return nil, err
		}
	}

	// Harvest proxy settings from admin.py.
	if proxy_url != nil {
		transport.Proxy = http.ProxyURL(proxy_url)
	}

	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: k.sslVerify}
	transport.DisableKeepAlives = true

	req, err := http.NewRequest("GET", target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", fmt.Sprintf("%s/%s", k.appName, k.localVer))

	client := &http.Client{Transport: &transport, Timeout: 0}
	return client.Do(req)
}
