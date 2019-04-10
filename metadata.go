package main

import (
	"text/template"
)

// Combile metadata information about file, append -info to the file and store it in path specified.
func (s *Session) MetaData(fname string, file_info *KiteData) (err error) {
	const metadata = `[file_metadata]
Filename = {{.Name}}
Uploader = {{.Creator}}
UploadedDate = {{.Created}}
Fingerprint = {{.Fingerprint}}
FileSize = {{.Size}}
MimeType = {{.Mime}}
`

	var creator_id int
	var found bool
	Uploader := "Anonymous/Unknown"

	for _, r := range file_info.Links {
		if r.Relationship == "creator" {
			creator_id = r.ID
			found = true
			break
		}
	}

	if found == true {
		creator, err := s.UserInfo(creator_id)
		if err == nil {
			Uploader = creator.Name
		}
	}

	var file_info_extended struct {
		Creator string
		*KiteData
	}

	file_info_extended.Creator = Uploader
	file_info_extended.KiteData = file_info

	t := template.Must(template.New("metadata").Parse(metadata))

	f, err := Create(fname + "-info")
	if err != nil {
		return err
	}

	err = t.Execute(f, file_info_extended)

	return
}
