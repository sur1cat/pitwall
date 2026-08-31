package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sur1cat/pitwall/internal/coach"
	"github.com/sur1cat/pitwall/internal/lint"
	"github.com/sur1cat/pitwall/internal/primer"
	"github.com/sur1cat/pitwall/internal/worktree"
)

// hookInput is the subset of Claude Code's hook payload pitwall reads.
type hookInput struct {
	HookEventName string `json:"hook_event_name"`
	CWD           string `json:"cwd"`
	SessionID     string `json:"session_id"`
	Prompt        string `json:"prompt"`
	WorktreePath  string `json:"worktree_path"`
	Path          string `json:"path"`
}

// hookOutput is the documented envelope a command hook writes to stdout.
type hookOutput struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext,omitempty"`
	} `json:"hookSpecificOutput"`
}

// cmdHook answers a Claude Code hook. It is deliberately silent unless it has
// something worth paying for: a session opening in a repository with no
// primer, where earlier sessions already learned the layout.
//
// A hook must never break a session, so every failure path prints an empty
// object and exits 0.
func cmdHook(args []string) error {
	event := "SessionStart"
	if len(args) > 0 {
		event = args[0]
	}
	silent := func() error {
		fmt.Println("{}")
		return nil
	}

	var in hookInput
	if err := json.NewDecoder(os.Stdin).Decode(&in); err != nil {
		return silent()
	}
	if event == "UserPromptSubmit" || in.HookEventName == "UserPromptSubmit" {
		return promptHook(in)
	}
	if event == "WorktreeCreate" || in.HookEventName == "WorktreeCreate" {
		return worktreeHook(in)
	}
	if in.CWD == "" {
		return silent()
	}
	repo := coach.RepoOf(in.CWD)
	if repo == "" || coach.Primed(repo) {
		return silent() // a CLAUDE.md already does this job, and does it better
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		return silent()
	}

	d, err := primer.Gather(repo)
	if err != nil || d.Sessions < 2 || d.ToolCalls < 50 {
		return silent() // too little history to be worth the tokens
	}
	ctx := d.Context()
	if ctx == "" {
		return silent()
	}

	var out hookOutput
	out.HookSpecificOutput.HookEventName = event
	out.HookSpecificOutput.AdditionalContext = ctx
	enc := json.NewEncoder(os.Stdout)
	return enc.Encode(out)
}

// promptHook tells the agent what this prompt leaves unstated, and asks it to
// assume rather than interrupt. It never rewrites what you typed.
func promptHook(in hookInput) error {
	findings := lint.Check(in.Prompt)
	ctx := lint.Context(findings)
	if ctx == "" {
		fmt.Println("{}")
		return nil
	}
	var out hookOutput
	out.HookSpecificOutput.HookEventName = "UserPromptSubmit"
	out.HookSpecificOutput.AdditionalContext = ctx
	return json.NewEncoder(os.Stdout).Encode(out)
}

// worktreeHook fills a brand-new worktree with the local config the main
// checkout has and git does not carry — the .env an agent needs before its
// first command can work.
func worktreeHook(in hookInput) error {
	target := in.WorktreePath
	if target == "" {
		target = in.Path
	}
	if target == "" {
		target = in.CWD
	}
	if target == "" {
		fmt.Println("{}")
		return nil
	}
	items, err := worktree.Prep(target, false)
	if err != nil || len(items) == 0 {
		fmt.Println("{}")
		return nil
	}
	var copied []string
	for _, i := range items {
		if i.Copied {
			copied = append(copied, i.Path)
		}
	}
	if len(copied) == 0 {
		fmt.Println("{}")
		return nil
	}
	var out hookOutput
	out.HookSpecificOutput.HookEventName = "WorktreeCreate"
	out.HookSpecificOutput.AdditionalContext = "pitwall copied local configuration into this " +
		"worktree from the main checkout, because git does not carry it: " +
		strings.Join(copied, ", ") + ". Ports and databases are still shared with the other " +
		"checkouts, so change them here if this worktree runs services."
	return json.NewEncoder(os.Stdout).Encode(out)
}
