package daemon_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Gitguy268/needle/core"
	"github.com/Gitguy268/needle/daemon"
	"github.com/Gitguy268/needle/internal/fake"
	"github.com/Gitguy268/needle/proto"
)

func TestListenCallQuit(t *testing.T) {
	dir, err := os.MkdirTemp("", "nd")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s")
	if _, err := daemon.Call(sock, proto.Command{Cmd: "status"}); err != daemon.ErrNotRunning {
		t.Fatalf("no daemon: %v", err)
	}
	p := core.NewPlayer(fake.New(), 40)
	defer p.Close()
	srv, err := daemon.Listen(sock, p)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	if info, _ := os.Stat(sock); info.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode %v", info.Mode().Perm())
	}
	if _, err := daemon.Listen(sock, p); err == nil {
		t.Fatal("a second listener on a live socket should fail")
	}
	r, err := daemon.Call(sock, proto.Command{Cmd: "status"})
	if err != nil || r.State.Volume != 40 {
		t.Fatalf("status: %+v %v", r, err)
	}
	if _, err := daemon.Call(sock, proto.Command{Cmd: "next"}); err == nil {
		t.Fatal("next on an empty queue should return the error")
	}
	if _, err := daemon.Call(sock, proto.Command{Cmd: "quit"}); err != nil {
		t.Fatal(err)
	}
	<-srv.Quit()
	if _, err := daemon.Listen(filepath.Join(dir, string(make([]byte, 120))), p); err == nil {
		t.Fatal("over-long socket path should fail")
	}
}
