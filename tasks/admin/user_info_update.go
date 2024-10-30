package admin

import (
	"bytes"
	"fmt"
	"github.com/cmcoffee/snugforge/csvp"
	"io/ioutil"
	. "kitebroker/core"
	"os"
	"strings"
)

type UserInfoTask struct {
	// input variables
	input struct {
		csv_file string
	}
	buffer bytes.Buffer
	// Required for all tasks
	KiteBrokerTask
}

func (T UserInfoTask) New() Task {
	return new(UserInfoTask)
}

func (T UserInfoTask) Name() string {
	return "update_user"
}

func (T UserInfoTask) Desc() string {
	return "Update user information."
}

func (T *UserInfoTask) Init() (err error) {
	T.Flags.Header = `Task updates user information, it takes a single csv.
csv should be in the format of: email,name,mobile number
`
	T.Flags.StringVar(&T.input.csv_file, "csv", "<user_info.csv>", "CSV to map user data, should be in format\n\temail,real name,phone")
	T.Flags.InlineArgs("csv")
	if err := T.Flags.Parse(); err != nil {
		return err
	}
	if IsBlank(T.input.csv_file) {
		return fmt.Errorf("Please provide a csv file to process.")
	}

	return
}

func (T *UserInfoTask) Main() (err error) {
	// Main function
	f, err := os.Open(T.input.csv_file)
	if err != nil {
		if os.IsNotExist(err) {
			Log(err)
			return nil
		}
		return err
	}

	c := csvp.NewReader()

	users_updated := make(map[string]interface{})

	clean_phone := func(input string) (output string) {
		input = strings.ReplaceAll(input, "(", "")
		input = strings.ReplaceAll(input, ")", "")
		input = strings.ReplaceAll(input, " ", "")
		input = strings.ReplaceAll(input, ".", "")
		if strings.HasPrefix(input, "+") {
			split_in := strings.Split(input, "-")
			if len(split_in) > 1 {
				input = fmt.Sprintf("%s-%s", split_in[0], strings.Join(split_in[1:], ""))
			}
		} else {
			input = strings.ReplaceAll(input, "-", "")
			input = fmt.Sprintf("+1-%s", input)
		}
		return input
	}

	c.Processor = func(row []string) (err error) {
		if len(row) < 3 {
			return fmt.Errorf("Invalid CSV, format must be: email,real name, phone")
		}

		email := row[0]
		name := row[1]
		phone := row[2]
		if !IsBlank(phone) {
			phone = clean_phone(phone)
		}

		if _, ok := users_updated[email]; ok {
			return nil
		} else {
			users_updated[email] = struct{}{}
		}

		Log("Updating user info for: %s <%s> - ph: %s", name, email, phone)
		T.buffer.WriteString(fmt.Sprintf("%s,%s,%s\n", email, name, phone))

		return nil
	}

	c.ErrorHandler = func(line int, input string, err error) (abort bool) {
		Err("%s: %s,", T.input.csv_file, err.Error())
		return false
	}

	T.buffer.WriteString("email,name,mobile\n")
	c.Read(f)

	output_csv := ioutil.NopCloser(&T.buffer)

	return T.KW.Admin().ImportUserMetadata(output_csv, true, false, true)
}
