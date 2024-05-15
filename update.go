package main

import (
	"net/http"
	"net/url"
	"crypto/tls"
	"strings"
	"fmt"
	. "github.com/cmcoffee/kitebroker/core"
	"github.com/cmcoffee/snugforge/options"
	"github.com/cmcoffee/snugforge/iotimeout"
	"time"
	"io/ioutil"
	"strconv"
	"runtime"
	"os"
	"io"
)

const update_server = "dist.snuglab.com"

func update_init() {
	update, version := check_for_update()
	if update {
		Log("### %s - Update Available!! ###\n\n", APPNAME)
		Log(" Local Version:\t%s", VERSION)
		Log("Remote Version:\t%s\n\n", version)
		if val := options.PromptBool("Perform update to the latest version?", true); val {
			update_self()
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
func update_self() {
	defer Exit(0)

	update_server := "dist.snuglab.com"
	build := fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH)

	resp, err := http_get(fmt.Sprintf("https://%s/kitebroker/%s/%s", update_server, build, os.Args[0]))
	if err != nil {
		Fatal(err)
	}
	defer resp.Body.Close()

	file_name := fmt.Sprintf("%s.%s", os.Args[0], build)
	dest_file_name := fmt.Sprintf("%s/%s.incomplete", global.root, file_name)

	os.Remove(dest_file_name)

	f, err := os.OpenFile(dest_file_name, os.O_CREATE|os.O_RDWR, 0775)
	Critical(err)

	Defer(func() { os.Remove(dest_file_name) })

	// If the filesize is different, online version is different.
	Log("\nDownloading latest %s update from %s...", APPNAME, update_server)
	src := TransferMonitor(file_name, resp.ContentLength, RightToLeft, NopSeekCloser(iotimeout.NewReadCloser(resp.Body, time.Minute)))

	io.Copy(f, src)

	f.Close()
	resp.Body.Close()
	src.Close()

	if err = os.Rename(dest_file_name, fmt.Sprintf("%s/%s", global.root, os.Args[0])); err != nil {
		Fatal(err)
	}

	Log("\n%s has been updated to the latest version.", APPNAME)
	return
}


func http_get(target string) (*http.Response, error) {
	var (
		transport http.Transport
		proxy_url *url.URL
		err error
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
