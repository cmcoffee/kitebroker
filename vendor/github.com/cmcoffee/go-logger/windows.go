// +build windows nacl plan9

package logger

type remote_blank struct{}

var remote_log *remote_blank

func (r remote_blank) Emerg(input string)   {}
func (r remote_blank) Info(input string)    {}
func (r remote_blank) Notice(input string)  {}
func (r remote_blank) Err(input string)     {}
func (r remote_blank) Warning(input string) {}
func (r remote_blank) Debug(input string)   {}
