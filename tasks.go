/*
	This is for registering the task modules to the kitebroker menu.
*/

package main

import (
	. "github.com/cmcoffee/kitebroker/core"

	_ "github.com/cmcoffee/kitebroker/tasks/admin/files_and_folders"
	_ "github.com/cmcoffee/kitebroker/tasks/admin/pubsub"
	_ "github.com/cmcoffee/kitebroker/tasks/admin/users"
	_ "github.com/cmcoffee/kitebroker/tasks/migration/kiteworks"
	_ "github.com/cmcoffee/kitebroker/tasks/migration/box"
	_ "github.com/cmcoffee/kitebroker/tasks/migration/quatrix"
	_ "github.com/cmcoffee/kitebroker/tasks/sync/kiteworks_mirror"
	_ "github.com/cmcoffee/kitebroker/tasks/user"
)

// loadTasks drains the core task registry and registers tasks with the command menu.
func loadTasks() {
	for _, t := range RegisteredMigrationTasks() {
		command.RegisterMigration(t)
	}
	for _, t := range RegisteredTasks() {
		command.Register(t)
	}
	for _, t := range RegisteredAdminTasks() {
		command.RegisterAdmin(t)
	}
	// Webhook-driven tasks are admin tasks too; they display alongside the
	// other admin tasks and are grouped by their "PubSub:" Desc prefix.
	for _, t := range RegisteredWebhookTasks() {
		command.RegisterAdmin(t)
	}
}
