package storage

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLinkOrCopyUsesHardLinkOnOneFileSystem(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source")
	destination := filepath.Join(directory, "destination")
	if err := os.WriteFile(source, []byte("guest-memory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := LinkOrCopy(context.Background(), source, destination); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(destination)
	if err != nil || string(content) != "guest-memory" {
		t.Fatalf("destination = %q, error %v", content, err)
	}
	sourceInformation, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	destinationInformation, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(sourceInformation, destinationInformation) {
		t.Error("local files do not share one inode")
	}
}

func TestKernelArgumentsUsesFileValueWhenPresent(t *testing.T) {
	directory := t.TempDir()
	if got := kernelArguments(directory); got != defaultKernelArguments {
		t.Errorf("default = %q", got)
	}
	if err := os.WriteFile(filepath.Join(directory, "boot-args"), []byte("custom args\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := kernelArguments(directory); got != "custom args" {
		t.Errorf("override = %q", got)
	}
}

func TestParseCloneList(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   []string
	}{
		{"no snapshots", "", nil},
		{"only empty markers", "-\n-\n", nil},
		{"one clone", "metal/staging/snap-1\n-\n", []string{"metal/staging/snap-1"}},
		{
			"multiple clones on one snapshot",
			"metal/staging/a,metal/staging/b\n",
			[]string{"metal/staging/a", "metal/staging/b"},
		},
		{
			"clones across snapshots with blanks",
			"metal/staging/a\n\n-\nmetal/staging/b\n",
			[]string{"metal/staging/a", "metal/staging/b"},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := parseCloneList(testCase.output); !slices.Equal(got, testCase.want) {
				t.Errorf("parseCloneList(%q) = %v, want %v", testCase.output, got, testCase.want)
			}
		})
	}
}
