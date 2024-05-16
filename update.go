package main

import (
	"crypto/tls"
	"fmt"
	. "github.com/cmcoffee/kitebroker/core"
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

const update_server = "dist.snuglab.com"

func update_init() {
	update, version := check_for_update()
	if update {
		Log("### %s - Update Available!! ###\n\n", APPNAME)
		Log(" Local Version:\t%s", VERSION)
		Log("Remote Version:\t%s\n\n", version)
		if val := options.PromptBool("Download latest version?", true); val {
			update_self(version)
		}
	} else {
		Log("%s is already at the latest version: %s.", APPNAME, VERSION)
	}
}

// Check if server has a newer version available.
func check_for_update() (bool, string) {
	resp, err := http_get(fmt.Sprintf("https://%s/kitebroker/version.txt", update_server))
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
	l_ver := cleanup_version(VERSION)

	if r_ver > l_ver {
		return true, string(remote_ver)
	}
	return false, VERSION
}

// Perform self update.
func update_self(new_version string) {
	defer Exit(0)

	current_os := runtime.GOOS

	update_server := "dist.snuglab.com"
	build := fmt.Sprintf("%s-%s", current_os, runtime.GOARCH)

	resp, err := http_get(fmt.Sprintf("https://%s/kitebroker/%s/%s", update_server, build, global.exec_name))
	if err != nil {
		Fatal(err)
	}
	defer resp.Body.Close()

	temp_file_name := NormalizePath(fmt.Sprintf("%s/%s.incomplete", global.root, fmt.Sprintf("%s.%s", global.exec_name, build)))

	os.Remove(temp_file_name)

	f, err := os.OpenFile(temp_file_name, os.O_CREATE|os.O_RDWR, 0775)
	Critical(err)

	// If the filesize is different, online version is different.
	Log("\nDownloading latest %s update from %s...", APPNAME, update_server)
	src := TransferMonitor("Download Update", resp.ContentLength, RightToLeft, NopSeekCloser(iotimeout.NewReadCloser(resp.Body, time.Minute)))

	Defer(func() { os.Remove(temp_file_name) })

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
		file_name := strings.Split(global.exec_name, ".")
		new_file_name := fmt.Sprintf("%s-%s.%s", file_name[0], new_version, file_name[1])
		dest_file = NormalizePath(fmt.Sprintf("%s/%s", global.root, new_file_name))
		final_msg = fmt.Sprintf("\nUpdate downloaded as %s.", dest_file)
	} else {
		dest_file = NormalizePath(fmt.Sprintf("%s/%s", global.root, global.exec_name))
		final_msg = fmt.Sprintf("\n%s has been updated to the latest version: %s", APPNAME, new_version)
	}

	if err = os.Rename(temp_file_name, dest_file); err != nil {
		Fatal(err)
	}

	Log(final_msg)
	return
}

func http_get(target string) (*http.Response, error) {
	var (
		transport http.Transport
		proxy_url *url.URL
		err       error
	)

	if proxy := global.cfg.Get("configuration", "proxy_uri"); !IsBlank(proxy) {
		proxy_url, err = url.Parse(strings.Join([]string{proxy}, ""))
		if err != nil {
			return nil, err
		}
	}

	// Harvest proxy settings from admin.py.
	if proxy_url != nil {
		transport.Proxy = http.ProxyURL(proxy_url)
	}

	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: !global.cfg.GetBool("configuration", "ssl_verify")}
	transport.DisableKeepAlives = true

	req, err := http.NewRequest("GET", target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", fmt.Sprintf("%s/%s", APPNAME, VERSION))

	client := &http.Client{Transport: &transport, Timeout: 0}
	return client.Do(req)
}
