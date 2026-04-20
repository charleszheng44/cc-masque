package docker

import (
	"reflect"
	"testing"
)

func TestBuildRunArgsDeterministic(t *testing.T) {
	uid, gid := 1234, 5678
	args := BuildRunArgs(RunSpec{
		Image: "img:tag",
		Name:  "ctr",
		UID:   &uid,
		GID:   &gid,
		Labels: map[string]string{
			"cc-crew.repo":  "acme/widget",
			"cc-crew.issue": "42",
		},
		Env: map[string]string{"FOO": "bar", "BAZ": "qux"},
		Mounts: []Mount{
			{HostPath: "/a", ContainerPath: "/workspace", ReadOnly: false},
			{HostPath: "/b", ContainerPath: "/b", ReadOnly: true},
		},
	})
	want := []string{
		"run", "--rm", "--name", "ctr",
		"--user", "1234:5678",
		"--label", "cc-crew.issue=42",
		"--label", "cc-crew.repo=acme/widget",
		"-e", "BAZ=qux", "-e", "FOO=bar",
		"-v", "/a:/workspace",
		"-v", "/b:/b:ro",
		"img:tag",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("got %v\nwant %v", args, want)
	}
}

// TestBuildRunArgsOmitsUserWhenUnset guards the back-compat path: callers that
// don't set UID/GID get docker's default user (image's USER directive or root).
func TestBuildRunArgsOmitsUserWhenUnset(t *testing.T) {
	args := BuildRunArgs(RunSpec{Image: "img", Name: "ctr"})
	for i, a := range args {
		if a == "--user" {
			t.Fatalf("unexpected --user at index %d in %v", i, args)
		}
	}
}

func TestParsePS(t *testing.T) {
	in := "ctr-a\timg\tcc-crew.repo=acme/widget,cc-crew.role=implementer\nctr-b\timg\t\n"
	entries := parsePS(in)
	if len(entries) != 2 {
		t.Fatalf("got %d entries", len(entries))
	}
	if entries[0].Labels["cc-crew.role"] != "implementer" {
		t.Fatalf("bad label parse: %+v", entries[0])
	}
}
