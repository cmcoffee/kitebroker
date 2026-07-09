package core

import (
	"reflect"
	"sync"
)

var (
	registryMu               sync.Mutex
	registeredTasks          []Task
	registeredAdminTasks     []Task
	registeredMigrationTasks []Task
)

// RegisterTask registers a universal task available to all auth modes.
func RegisterTask(task Task) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registeredTasks = append(registeredTasks, task)
}

// RegisterAdminTask registers an admin-only task (signature/jwt auth).
func RegisterAdminTask(task Task) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registeredAdminTasks = append(registeredAdminTasks, task)
}

// RegisterMigrationTask registers a migration task.
func RegisterMigrationTask(task Task) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registeredMigrationTasks = append(registeredMigrationTasks, task)
}

// RegisteredTasks returns all registered universal tasks.
func RegisteredTasks() []Task { return registeredTasks }

// RegisteredAdminTasks returns all registered admin tasks.
func RegisteredAdminTasks() []Task { return registeredAdminTasks }

// RegisteredMigrationTasks returns all registered migration tasks.
func RegisteredMigrationTasks() []Task { return registeredMigrationTasks }

// LookupTask returns a fresh instance of the registered task whose Name()
// matches name, searching universal, admin, and migration registries in that
// order. It clones the registered prototype (the same approach the menu uses
// when running a task more than once) so callers get a clean instance with
// independent state. It returns nil when no task matches.
func LookupTask(name string) Task {
	registryMu.Lock()
	defer registryMu.Unlock()

	for _, group := range [][]Task{registeredTasks, registeredAdminTasks, registeredMigrationTasks} {
		for _, t := range group {
			if t.Name() == name {
				return reflect.New(reflect.TypeOf(t).Elem()).Interface().(Task)
			}
		}
	}
	return nil
}
