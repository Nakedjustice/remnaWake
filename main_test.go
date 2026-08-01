package main

import "testing"

func TestUserBotCommandsIncludeTrial(t *testing.T) {
	cmds := userBotCommands()
	for _, cmd := range cmds {
		if cmd.Command == "trial" {
			return
		}
	}
	t.Fatal("user bot commands must include /trial")
}

// Device self-management must be reachable without the Mini App, so it needs a
// chat command of its own alongside the cabinet button.
func TestUserBotCommandsIncludeDevices(t *testing.T) {
	for _, cmd := range userBotCommands() {
		if cmd.Command == "devices" {
			return
		}
	}
	t.Fatal("user bot commands must include /devices")
}
