// +build !windows,!nacl,!plan9

package logger

import (
	"log/syslog"
)

var remote_log *syslog.Writer

// Opens connection to remote syslog, tag specifies application name that will be prefixed to syslog entry.
// ex: logger.RemoteSyslog("udp", "192.168.0.4:514", "application_name")
func Syslog(network, raddr string, tag string) {
	syslog, err := syslog.Dial(network, raddr, syslog.LOG_DAEMON, tag)
	if err != nil {
		Err("Error enabling syslog to %s: %s", raddr, err.Error())
		return
	}
	remote_log = syslog
	Trace("Syslog initialized to %s.", raddr)
}

// Close connection to remote syslog.
func CloseSyslog() {
	if remote_log == nil {
		return
	}
	remote_log.Close()
	Trace("Closed connection to syslog.")
}
