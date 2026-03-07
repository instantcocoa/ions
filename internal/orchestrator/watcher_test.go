package orchestrator

import (
	"testing"

	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/assert"
)

func TestShouldIgnoreDir(t *testing.T) {
	tests := []struct {
		name   string
		ignore bool
	}{
		{".git", true},
		{".ions-work", true},
		{".letta", true},
		{"node_modules", true},
		{"__pycache__", true},
		{".next", true},
		{"dist", true},
		{"build", true},
		{".cache", true},
		{".venv", true},
		{"venv", true},
		{"src", false},
		{"internal", false},
		{"cmd", false},
		{".github", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.ignore, shouldIgnoreDir(tt.name))
		})
	}
}

func TestIsRelevantChange(t *testing.T) {
	repoPath := "/home/user/project"

	tests := []struct {
		name     string
		event    fsnotify.Event
		relevant bool
	}{
		{
			"write to source file",
			fsnotify.Event{Name: "/home/user/project/main.go", Op: fsnotify.Write},
			true,
		},
		{
			"create new file",
			fsnotify.Event{Name: "/home/user/project/new.go", Op: fsnotify.Create},
			true,
		},
		{
			"delete file",
			fsnotify.Event{Name: "/home/user/project/old.go", Op: fsnotify.Remove},
			true,
		},
		{
			"git internal file",
			fsnotify.Event{Name: "/home/user/project/.git/objects/abc123", Op: fsnotify.Write},
			false,
		},
		{
			"node_modules change",
			fsnotify.Event{Name: "/home/user/project/node_modules/foo/index.js", Op: fsnotify.Write},
			false,
		},
		{
			"vim swap file",
			fsnotify.Event{Name: "/home/user/project/.main.go.swp", Op: fsnotify.Write},
			false,
		},
		{
			"editor backup file",
			fsnotify.Event{Name: "/home/user/project/main.go~", Op: fsnotify.Write},
			false,
		},
		{
			"emacs autosave",
			fsnotify.Event{Name: "/home/user/project/#main.go#", Op: fsnotify.Write},
			false,
		},
		{
			"ions work dir",
			fsnotify.Event{Name: "/home/user/project/.ions-work/build/file", Op: fsnotify.Write},
			false,
		},
		{
			"chmod only",
			fsnotify.Event{Name: "/home/user/project/main.go", Op: fsnotify.Chmod},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.relevant, isRelevantChange(tt.event, repoPath))
		})
	}
}
