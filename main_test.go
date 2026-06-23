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
