package nfo

var export_syslog SyslogWriter

// Interface for log/syslog/Writer.
type SyslogWriter interface {
	Alert(string) error
	Crit(string) error
	Debug(string) error
	Emerg(string) error
	Err(string) error
	Info(string) error
	Notice(string) error
	Warning(string) error
}

// Send messages to syslog
func HookSyslog(syslog_writer SyslogWriter) {
	mutex.Lock()
	defer mutex.Unlock()
	export_syslog = syslog_writer
}

// Disconnect form syslog
func UnhookSyslog() {
	mutex.Lock()
	defer mutex.Unlock()
	export_syslog = nil
}
