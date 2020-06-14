/*
	This is for registering the task modules to the kitebroker menu.
*/

package main

import (
	. "github.com/cmcoffee/kitebroker/tasks"
)

func init() {
	jobs.RegisterAdmin("user_profiler", "Change user profile based on last activity date.", new(UserProfilerTask))
}
