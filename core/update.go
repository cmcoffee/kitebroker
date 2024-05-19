package core

import (
	"crypto/tls"
	"fmt"
	"github.com/cmcoffee/snugforge/iotimeout"
	"github.com/cmcoffee/snugforge/options"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// kitebrokerUpdater Object
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

// Updates kitebroker app.
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
	if update_avail {
		Log("### %s - Update Available!! ###\n\n", k.appName)
		Log(" Local Version:\t%s", k.localVer)
		Log("Remote Version:\t%s\n\n", k.remoteVer)
		if val := options.PromptBool("Download latest version?", true); val {
			k.update_self()
		}
	} else {
		Log("%s is already at the latest version: %s.", k.appName, k.remoteVer)
	}
}

// Check if server has a newer version available.
func (k kitebrokerUpdater) check_for_update() (bool, string) {
	resp, err := k.http_get(fmt.Sprintf("https://%s/kitebroker/version.txt", k.updateServer))
	if err != nil {
		Fatal(err)
	}
	defer resp.Body.Close()
	remote_ver, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		Fatal(err)
	}

	cleanup_version := func(input string) int64 {
		cinput := strings.Replace(input, ".", "", -1)
		cinput = strings.Replace(cinput, "-", "", -1)
		num, err := strconv.ParseInt(cinput, 10, 64)
		if err != nil {
			Fatal(fmt.Errorf("Unable to read remote server version: %s", input))
		}
		return num
	}

	r_ver := cleanup_version(string(remote_ver))
	l_ver := cleanup_version(k.localVer)

	if r_ver > l_ver {
		return true, string(remote_ver)
	}
	return false, k.localVer
}

// Perform self update.
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
	src := transferMonitor("Download Update", resp.ContentLength, rightToLeft|limitWidth, nopSeeker(iotimeout.NewReadCloser(resp.Body, time.Minute)))

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

	if current_os == "windows" {
		file_name := strings.Split(k.localExec, ".")
		new_file_name := fmt.Sprintf("%s-%s.%s", file_name[0], k.remoteVer, file_name[1])
		dest_file = LocalPath(fmt.Sprintf("%s/%s", k.localPath, new_file_name))
		final_msg = fmt.Sprintf("\nUpdate downloaded as %s.", dest_file)
	} else {
		dest_file = LocalPath(fmt.Sprintf("%s/%s", k.localPath, k.localExec))
		final_msg = fmt.Sprintf("\n%s has been updated to the latest version: %s", k.appName, k.remoteVer)
	}

	if err = os.Rename(temp_file_name, dest_file); err != nil {
		Fatal(err)
	}

	Log(final_msg)
	return
}

// Web get function.
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
