package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/fsnotify/fsnotify"
)

// WatchAndRun watches for file changes in the repo and re-runs the workflow.
// It debounces rapid changes and cancels in-flight runs when new changes arrive.
func WatchAndRun(ctx context.Context, opts Options) error {
	bold := color.New(color.Bold)
	dim := color.New(color.Faint)
	green := color.New(color.FgGreen)
	yellow := color.New(color.FgYellow)

	repoPath := opts.RepoPath
	if repoPath == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("cannot determine working directory: %w", err)
		}
		repoPath = cwd
		opts.RepoPath = repoPath
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("cannot create file watcher: %w", err)
	}
	defer watcher.Close()

	// Recursively add directories to the watcher.
	if err := addWatchDirs(watcher, repoPath); err != nil {
		return fmt.Errorf("cannot watch directory: %w", err)
	}

	bold.Println("Watch mode: waiting for file changes...")
	dim.Printf("  watching: %s\n", repoPath)
	dim.Printf("  workflow: %s\n", opts.WorkflowPath)
	fmt.Println()

	// Run once immediately.
	runCtx, runCancel := context.WithCancel(ctx)
	var runWg sync.WaitGroup
	runWg.Add(1)
	go func() {
		defer runWg.Done()
		runOnce(runCtx, opts, 1)
	}()

	runNumber := 1
	var debounceTimer *time.Timer

	for {
		select {
		case <-ctx.Done():
			runCancel()
			runWg.Wait()
			return nil

		case event, ok := <-watcher.Events:
			if !ok {
				runCancel()
				runWg.Wait()
				return nil
			}

			if !isRelevantChange(event, repoPath) {
				continue
			}

			// If a new directory was created, watch it too.
			if event.Has(fsnotify.Create) {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					addWatchDirs(watcher, event.Name)
				}
			}

			// Debounce: reset the timer on each change event.
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			debounceTimer = time.AfterFunc(500*time.Millisecond, func() {
				// Cancel the current run.
				runCancel()
				runWg.Wait()

				runNumber++
				rel, _ := filepath.Rel(repoPath, event.Name)
				if rel == "" {
					rel = event.Name
				}
				fmt.Println()
				yellow.Printf("File changed: %s\n", rel)
				green.Printf("Re-running workflow (run #%d)...\n", runNumber)
				fmt.Println()

				// Start a new run.
				runCtx, runCancel = context.WithCancel(ctx)
				runWg.Add(1)
				go func() {
					defer runWg.Done()
					runOnce(runCtx, opts, runNumber)
				}()
			})

		case err, ok := <-watcher.Errors:
			if !ok {
				runCancel()
				runWg.Wait()
				return nil
			}
			fmt.Fprintf(os.Stderr, "watcher error: %v\n", err)
		}
	}
}

// runOnce executes the workflow once, logging the result.
func runOnce(ctx context.Context, opts Options, runNumber int) {
	green := color.New(color.FgGreen)
	red := color.New(color.FgRed)

	o, err := New(opts)
	if err != nil {
		red.Printf("Error: %v\n", err)
		return
	}

	result, err := o.Run(ctx)
	if err != nil {
		if ctx.Err() != nil {
			// Cancelled — don't print error.
			return
		}
		red.Printf("Error: %v\n", err)
		return
	}

	fmt.Println()
	if result.Success {
		green.Printf("Run #%d succeeded (%s)\n", runNumber, formatDuration(result.Duration))
	} else {
		red.Printf("Run #%d failed (%s)\n", runNumber, formatDuration(result.Duration))
	}
}

// addWatchDirs recursively adds directories to the watcher, skipping
// ignored directories.
func addWatchDirs(watcher *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible dirs
		}
		if !d.IsDir() {
			return nil
		}

		name := d.Name()
		if shouldIgnoreDir(name) {
			return filepath.SkipDir
		}

		return watcher.Add(path)
	})
}

// shouldIgnoreDir returns true for directories that should not be watched.
func shouldIgnoreDir(name string) bool {
	switch name {
	case ".git", ".ions-work", ".letta", "node_modules", "__pycache__",
		".next", ".nuxt", "dist", "build", ".cache", ".venv", "venv":
		return true
	}
	return false
}

// isRelevantChange returns true if a filesystem event should trigger a re-run.
func isRelevantChange(event fsnotify.Event, repoPath string) bool {
	// Only care about write, create, remove, rename events.
	if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) &&
		!event.Has(fsnotify.Remove) && !event.Has(fsnotify.Rename) {
		return false
	}

	// Ignore paths within ignored directories.
	rel, err := filepath.Rel(repoPath, event.Name)
	if err != nil {
		return false
	}

	parts := strings.Split(rel, string(filepath.Separator))
	for _, part := range parts {
		if shouldIgnoreDir(part) {
			return false
		}
	}

	// Ignore temporary/editor files.
	base := filepath.Base(event.Name)
	if strings.HasPrefix(base, ".") && strings.HasSuffix(base, ".swp") {
		return false
	}
	if strings.HasSuffix(base, "~") {
		return false
	}
	if strings.HasPrefix(base, "#") && strings.HasSuffix(base, "#") {
		return false
	}

	return true
}
