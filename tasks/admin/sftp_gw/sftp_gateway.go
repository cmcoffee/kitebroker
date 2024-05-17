package sftp_gw

import (
	. "github.com/cmcoffee/kitebroker/core"
	"golang.org/x/crypto/ssh"
	//"net"
)

type SFTPGWTask struct {
	// input variables
	input struct {
		input_flag string
	}
	// Required for all tasks
	KiteBrokerTask
}

type SSHServ struct {
	KW          KWSession
	server_conf ssh.ServerConfig
}

func (S *SSHServ) PasswordCallback(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
	user, err := S.KW.Admin().FindUser(conn.User())
	if err != nil {
		return nil, err
	}
	Log(user)
	return nil, nil
}

func (S *SSHServ) PublicKeyCallback(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	user, err := S.KW.Admin().FindUser(conn.User())
	if err != nil {
		return nil, err
	}
	Log(user)
	return nil, nil
}

func (S *SSHServ) ConfigureServer(admin KWSession) {
	S.KW = admin
	S.server_conf.PasswordCallback = S.PasswordCallback
}

func (T SFTPGWTask) New() Task {
	return new(SFTPGWTask)
}

func (T SFTPGWTask) Name() string {
	return "sftp_gw"
}

func (T *SFTPGWTask) Desc() string {
	return ""
}

func (T *SFTPGWTask) Init() (err error) {
	T.Flags.StringVar(&T.input.input_flag, "flag", "<example text>", "Example string value")
	T.Flags.Order("flag")
	if err := T.Flags.Parse(); err != nil {
		return err
	}

	return
}

func (T *SFTPGWTask) Main() (err error) {
	return nil
}
