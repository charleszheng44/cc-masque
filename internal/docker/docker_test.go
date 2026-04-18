package docker

import (
	"reflect"
	"testing"
)

func TestBuildRunArgsDeterministic(t *testing.T) {
	args := BuildRunArgs(RunSpec{
		Image: "img:tag",
		Name:  "ctr",
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
