package main

import "testing"

func TestPublicCommandSet(t *testing.T) {
	want := map[string]bool{
		"configure": true, "inspect": true, "peers": true, "privileged-vm": true,
		"reset": true, "status": true, "version": true, "vm": true,
	}
	got := make(map[string]bool)
	for _, command := range rootCommand.Commands() {
		got[command.Name()] = true
	}
	for command := range want {
		if !got[command] {
			t.Errorf("missing command %s", command)
		}
	}
	for _, removed := range []string{"debug", "gateway", "unicast", "upgrade"} {
		if got[removed] {
			t.Errorf("removed command %s is still present", removed)
		}
	}
}
