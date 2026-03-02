package core

import "sync"

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
