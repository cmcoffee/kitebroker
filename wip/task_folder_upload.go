package main

import (
	//"fmt"
	//. "github.com/cmcoffee/go-kwlib"
	//"time"
	//"os"
	//"io"
	//"sync/atomic"
	//"sync"
)

func init() {
	glo.menu.Register("folder_upload", "Upload files & folders to kiteworks.", new(FolderUpload))
}

type FolderUpload struct {

}

func (T *FolderUpload) New() task {
	return new(FolderUpload)
}

func (T *FolderUpload) Init(flag *FlagSet) (err error) {
	return nil
}

func (T *FolderUpload) Main() (err error) {
	return nil
}