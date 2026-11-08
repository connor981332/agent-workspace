package platform

import (
	"os"
	"os/exec"
	"testing"
)

func TestProcessIdentity(t *testing.T) {
	self, err := ProcessIdentity(os.Getpid())
	if err != nil || self == "" {
		t.Fatalf("own identity: %q, %v", self, err)
	}
	again, _ := ProcessIdentity(os.Getpid())
	if again != self {
		t.Errorf("identity changed: %q then %q", self, again)
	}
	if !Alive(os.Getpid(), self) {
		t.Errorf("this process is not alive")
	}
	if Alive(os.Getpid(), self+"x") {
		t.Errorf("a different identity is alive")
	}

	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	child, err := ProcessIdentity(cmd.Process.Pid)
	if err != nil || child == "" {
		t.Fatalf("child identity %q, %v", child, err)
	}
	cmd.Process.Kill()
	cmd.Wait()
	if Alive(cmd.Process.Pid, child) {
		t.Errorf("a dead child is alive")
	}
	if !Alive(0, "") {
		t.Errorf("an unknown process must count as alive")
	}
}
