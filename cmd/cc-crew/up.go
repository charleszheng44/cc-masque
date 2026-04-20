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
	"github.com/charleszheng44/cc-crew/internal/continuous"
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
			MaxTurns:    c.ImplMaxTurns,
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
			MaxTurns:    c.ReviewMaxTurns,
			TaskTimeout: c.ReviewTaskTimeout,
			RoleGHToken: c.ReviewerGHToken, ClaudeOAuth: c.ClaudeOAuthToken, AnthropicAPIKey: c.AnthropicAPIKey,
			GitName: c.ReviewerGitName, GitEmail: c.ReviewerGitEmail,
			MergeLabel:   c.MergeLabel,
			AddressLabel: c.AddressLabel,
		}
		s := &scheduler.Scheduler{
			Kind: claim.KindReviewer, Sem: scheduler.NewSemaphore(c.MaxReviewers),
			Claimer: claimer, GH: ghc, Repo: repo, Dispatcher: lc, Log: log,
			QueueLabel: c.ReviewLabel, LockLabel: c.ReviewingLabel,
		}
		schedulers = append(schedulers, s)
	}

	if c.MaxImplementers > 0 {
		addr := &scheduler.Lifecycle{
			Kind: claim.KindAddresser, Claimer: claimer, GH: ghc, Repo: repo,
			WT: wt, Docker: dr, Log: log,
			QueueLabel:  c.AddressLabel,
			LockLabel:   c.AddressingLabel,
			DoneLabel:   c.AddressedLabel,
			Image:       c.Image,
			Model:       c.Model,
			MaxTurns:    c.ImplMaxTurns,
			TaskTimeout: c.ImplTaskTimeout,
			RoleGHToken: c.ImplementerGHToken, ClaudeOAuth: c.ClaudeOAuthToken, AnthropicAPIKey: c.AnthropicAPIKey,
			GitName: c.ImplementerGitName, GitEmail: c.ImplementerGitEmail,
			BaseBranch: c.BaseBranch,
		}
		addrSched := &scheduler.Scheduler{
			Kind: claim.KindAddresser, Sem: schedulers[0].Sem, // REUSE implementer semaphore
			Claimer: claimer, GH: ghc, Repo: repo, Dispatcher: addr, Log: log,
			QueueLabel: c.AddressLabel, LockLabel: c.AddressingLabel,
		}
		schedulers = append(schedulers, addrSched)
	}

	if c.MaxMergers > 0 {
		mergerLC := &scheduler.Lifecycle{
			Kind: claim.KindMerger, Claimer: claimer, GH: ghc, Repo: repo,
			Log:                  log,
			QueueLabel:           c.MergeLabel,
			LockLabel:            c.MergingLabel,
			ResolveConflictLabel: c.ResolveConflictLabel,
			ConflictBlockedLabel: c.ConflictBlockedLabel,
			RoleGHToken:          c.ReviewerGHToken,
		}
		mergerS := &scheduler.Scheduler{
			Kind: claim.KindMerger, Sem: scheduler.NewSemaphore(c.MaxMergers),
			Claimer: claimer, GH: ghc, Repo: repo, Dispatcher: mergerLC, Log: log,
			QueueLabel: c.MergeLabel, LockLabel: c.MergingLabel,
		}
		schedulers = append(schedulers, mergerS)

		// Resolver: requires implementer to be enabled (shares its semaphore
		// and uses its image). Otherwise conflicts cannot be resolved.
		if c.MaxImplementers > 0 {
			resolverLC := &scheduler.Lifecycle{
				Kind: claim.KindResolver, Claimer: claimer, GH: ghc, Repo: repo,
				WT: wt, Docker: dr, Log: log,
				QueueLabel:           c.ResolveConflictLabel,
				LockLabel:            c.ResolvingLabel,
				ReviewLabel:          c.ReviewLabel,
				DoneLabel:            c.ReviewedLabel,
				MergeLabel:           c.MergeLabel,
				ConflictBlockedLabel: c.ConflictBlockedLabel,
				Image:                c.Image,
				Model:                c.Model,
				MaxTurns:             c.ImplMaxTurns,
				TaskTimeout:          c.ImplTaskTimeout,
				RoleGHToken:          c.ImplementerGHToken,
				ClaudeOAuth:          c.ClaudeOAuthToken,
				AnthropicAPIKey:      c.AnthropicAPIKey,
				GitName:              c.ImplementerGitName,
				GitEmail:             c.ImplementerGitEmail,
			}
			resolverS := &scheduler.Scheduler{
				Kind: claim.KindResolver, Sem: schedulers[0].Sem, // share implementer semaphore
				Claimer: claimer, GH: ghc, Repo: repo, Dispatcher: resolverLC, Log: log,
				QueueLabel: c.ResolveConflictLabel, LockLabel: c.ResolvingLabel,
			}
			schedulers = append(schedulers, resolverS)
		} else {
			log.Warn("max-mergers > 0 but max-implementers == 0; resolver disabled (conflicts will be terminal)")
		}
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
	addrSweeper := &reclaim.Sweeper{
		GH: ghc, Repo: repo, Claimer: claimer,
		Kind:   claim.KindAddresser,
		MaxAge: c.ReclaimAfter,
		// Addresser has no "already-done" short-circuit; a stale lock just means
		// the dispatch crashed. Always reap.
		IsDone: func(ctx context.Context, n int) (bool, error) { return false, nil },
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
			if err := addrSweeper.Sweep(ctx); err != nil {
				log.Warn("addr reclaim", "err", err)
			}
			if c.Continuous {
				res, err := continuous.Detect(ctx, continuous.Options{
					GH: ghc, Repo: repo, MaxCycles: c.MaxCycles,
					Labels: continuous.Labels{
						Review:     c.ReviewLabel,
						Reviewing:  c.ReviewingLabel,
						Reviewed:   c.ReviewedLabel,
						Address:    c.AddressLabel,
						Addressing: c.AddressingLabel,
					},
				})
				if err != nil {
					log.Warn("continuous detect", "err", err)
				} else if res.ReviewFlipped > 0 || res.AddressLabeled > 0 {
					log.Info("continuous", "reviews_flipped", res.ReviewFlipped, "address_labeled", res.AddressLabeled)
				}
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

	// Best-effort: release labels/claims so the next orchestrator start can
	// immediately retry rather than waiting for reclaim.
	for _, e := range entries {
		role := e.Labels["cc-crew.role"]
		switch role {
		case "implementer":
			// Addresser runs as implementer; distinguish by cc-crew.mode.
			if e.Labels["cc-crew.mode"] == "address" {
				prStr := e.Labels["cc-crew.pr"]
				var n int
				if _, err := fmt.Sscan(prStr, &n); err != nil || n == 0 {
					continue
				}
				if err := ghc.RemoveLabel(shutCtx, repo, n, c.AddressingLabel); err != nil {
					log.Warn("sigint cleanup: remove addressing label", "pr", n, "err", err)
				}
				if err := claimer.Release(shutCtx, claim.KindAddresser, n, true); err != nil {
					log.Warn("sigint cleanup: release addresser claim", "pr", n, "err", err)
				}
				continue
			}
			issueStr := e.Labels["cc-crew.issue"]
			var n int
			if _, err := fmt.Sscan(issueStr, &n); err != nil || n == 0 {
				continue
			}
			if err := ghc.RemoveLabel(shutCtx, repo, n, c.ProcessingLabel); err != nil {
				log.Warn("sigint cleanup: remove processing label", "issue", n, "err", err)
			}
			if err := claimer.Release(shutCtx, claim.KindImplementer, n, true); err != nil {
				log.Warn("sigint cleanup: release implementer claim", "issue", n, "err", err)
			}
		case "reviewer":
			prStr := e.Labels["cc-crew.pr"]
			var n int
			if _, err := fmt.Sscan(prStr, &n); err != nil || n == 0 {
				continue
			}
			if err := ghc.RemoveLabel(shutCtx, repo, n, c.ReviewingLabel); err != nil {
				log.Warn("sigint cleanup: remove reviewing label", "pr", n, "err", err)
			}
			if err := claimer.Release(shutCtx, claim.KindReviewer, n, true); err != nil {
				log.Warn("sigint cleanup: release reviewer claim", "pr", n, "err", err)
			}
		case "resolver":
			prStr := e.Labels["cc-crew.pr"]
			var n int
			if _, err := fmt.Sscan(prStr, &n); err != nil || n == 0 {
				continue
			}
			if err := ghc.RemoveLabel(shutCtx, repo, n, c.ResolvingLabel); err != nil {
				log.Warn("sigint cleanup: remove resolving label", "pr", n, "err", err)
			}
			if err := claimer.Release(shutCtx, claim.KindResolver, n, true); err != nil {
				log.Warn("sigint cleanup: release resolver claim", "pr", n, "err", err)
			}
		}
	}

	log.Info("bye")
	return 0
}
