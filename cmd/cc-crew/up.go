package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/charleszheng44/cc-crew/internal/claim"
	"github.com/charleszheng44/cc-crew/internal/config"
	"github.com/charleszheng44/cc-crew/internal/docker"
	"github.com/charleszheng44/cc-crew/internal/github"
	"github.com/charleszheng44/cc-crew/internal/reclaim"
	"github.com/charleszheng44/cc-crew/internal/scheduler"
	"github.com/charleszheng44/cc-crew/internal/worktree"
)

func runUp(args []string) int {
	pwd, _ := os.Getwd()
	c, err := config.Parse(args, os.Getenv, pwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if err := c.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ghc := github.NewGhClient()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	owner, name, err := config.ResolveRepo(ctx, c.RepoDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	repo := github.Repo{Owner: owner, Name: name}
	log.Info("cc-crew starting", "repo", repo.String(), "dir", c.RepoDir,
		"max_impl", c.MaxImplementers, "max_rev", c.MaxReviewers)

	if c.BaseBranch == "" {
		b, err := ghc.DefaultBranch(ctx, repo)
		if err != nil {
			fmt.Fprintln(os.Stderr, "resolve default branch:", err)
			return 1
		}
		c.BaseBranch = b
	}

	reviewerLogin := ""
	if c.MaxReviewers > 0 {
		login, err := ghc.CurrentUser(ctx)
		if err != nil {
			log.Warn("couldn't resolve reviewer login; reclaim already-done check disabled", "err", err)
		} else {
			reviewerLogin = login
		}
	}

	wt := worktree.New(c.RepoDir)
	dr := docker.New()
	claimer := claim.New(ghc, repo)

	var schedulers []*scheduler.Scheduler

	if c.MaxImplementers > 0 {
		lc := &scheduler.Lifecycle{
			Kind: claim.KindImplementer, Claimer: claimer, GH: ghc, Repo: repo,
			WT: wt, Docker: dr, Log: log,
			QueueLabel:  c.TaskLabel,
			LockLabel:   c.ProcessingLabel,
			DoneLabel:   c.DoneLabel,
			ReviewLabel: c.ReviewLabel,
			Image:       c.Image,
			Model:       c.Model,
			MaxTurns:    25,
			TaskTimeout: c.ImplTaskTimeout,
			AutoReview:  c.AutoReview,
			RoleGHToken: c.ImplementerGHToken, ClaudeOAuth: c.ClaudeOAuthToken, AnthropicAPIKey: c.AnthropicAPIKey,
			GitName: c.ImplementerGitName, GitEmail: c.ImplementerGitEmail,
			BaseBranch: c.BaseBranch,
		}
		s := &scheduler.Scheduler{
			Kind: claim.KindImplementer, Sem: scheduler.NewSemaphore(c.MaxImplementers),
			Claimer: claimer, GH: ghc, Repo: repo, Dispatcher: lc, Log: log,
			QueueLabel: c.TaskLabel, LockLabel: c.ProcessingLabel,
		}
		schedulers = append(schedulers, s)
	}

	if c.MaxReviewers > 0 {
		lc := &scheduler.Lifecycle{
			Kind: claim.KindReviewer, Claimer: claimer, GH: ghc, Repo: repo,
			WT: wt, Docker: dr, Log: log,
			QueueLabel:  c.ReviewLabel,
			LockLabel:   c.ReviewingLabel,
			DoneLabel:   c.ReviewedLabel,
			Image:       c.Image,
			Model:       c.Model,
			MaxTurns:    15,
			TaskTimeout: c.ReviewTaskTimeout,
			RoleGHToken: c.ReviewerGHToken, ClaudeOAuth: c.ClaudeOAuthToken, AnthropicAPIKey: c.AnthropicAPIKey,
			GitName: c.ReviewerGitName, GitEmail: c.ReviewerGitEmail,
		}
		s := &scheduler.Scheduler{
			Kind: claim.KindReviewer, Sem: scheduler.NewSemaphore(c.MaxReviewers),
			Claimer: claimer, GH: ghc, Repo: repo, Dispatcher: lc, Log: log,
			QueueLabel: c.ReviewLabel, LockLabel: c.ReviewingLabel,
		}
		schedulers = append(schedulers, s)
	}

	implSweeper := &reclaim.Sweeper{
		GH: ghc, Repo: repo, Claimer: claimer,
		Kind:   claim.KindImplementer,
		MaxAge: c.ReclaimAfter,
		IsDone: reclaim.ImplementerDoneFn(ghc, repo),
		Now:    time.Now,
	}
	revSweeper := &reclaim.Sweeper{
		GH: ghc, Repo: repo, Claimer: claimer,
		Kind:   claim.KindReviewer,
		MaxAge: c.ReclaimAfter,
		IsDone: reclaim.ReviewerDoneFn(ghc, repo, reviewerLogin),
		Now:    time.Now,
	}

	go func() {
		t := time.NewTicker(c.PollInterval)
		defer t.Stop()
		for {
			if err := implSweeper.Sweep(ctx); err != nil {
				log.Warn("impl reclaim", "err", err)
			}
			if err := revSweeper.Sweep(ctx); err != nil {
				log.Warn("rev reclaim", "err", err)
			}
			for _, s := range schedulers {
				if err := s.Tick(ctx); err != nil {
					log.Warn("tick", "kind", s.Kind, "err", err)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()

	<-ctx.Done()
	log.Info("shutdown requested; stopping tasks")

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutCancel()
	entries, _ := dr.PS(shutCtx, map[string]string{"cc-crew.repo": repo.String()})
	var wg sync.WaitGroup
	for _, e := range entries {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			_ = dr.Kill(shutCtx, name)
		}(e.Name)
	}
	wg.Wait()
	log.Info("bye")
	return 0
}
