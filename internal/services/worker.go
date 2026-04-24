package services

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"

	"github.com/velox0/kraken/internal/autofix"
	"github.com/velox0/kraken/internal/db"
	"github.com/velox0/kraken/internal/incident"
	"github.com/velox0/kraken/internal/monitor"
	"github.com/velox0/kraken/internal/queue"
)

type Worker struct {
	Store         *db.Store
	Queue         *queue.RedisQueue
	AutofixEngine *autofix.Engine
	Incident      *incident.Service
	Log           *log.Logger
}

func (w *Worker) Run(ctx context.Context) {
	if w.Log == nil {
		w.Log = log.Default()
	}
	w.Log.Println("worker started")

	for {
		select {
		case <-ctx.Done():
			w.Log.Println("worker stopping")
			return
		default:
		}

		fixJob, err := w.Queue.DequeueFix(ctx, 1*time.Second)
		if err == nil {
			w.handleFixJob(ctx, fixJob)
			continue
		}
		if err != queue.ErrNoJob {
			w.Log.Printf("dequeue fix failed: %v", err)
		}

		job, err := w.Queue.DequeueCheck(ctx, 4*time.Second)
		if err != nil {
			if err == queue.ErrNoJob {
				continue
			}
			w.Log.Printf("dequeue check failed: %v", err)
			continue
		}
		w.handleCheckJob(ctx, job)
	}
}

func (w *Worker) handleCheckJob(ctx context.Context, job queue.CheckJob) {
	checkCtx, err := w.Store.GetCheckContext(ctx, job.CheckID)
	if err != nil {
		w.Log.Printf("load check context failed: %v", err)
		return
	}

	// Build effective target: domain + route.
	// The check target stores just the route (e.g. "/" or "/api"),
	// and the project domain provides the host (e.g. "localhost:3000").
	effectiveTarget := buildEffectiveTarget(checkCtx.Type, checkCtx.ProjectDomain, checkCtx.Target)

	result := monitor.RunCheck(ctx, checkCtx.Type, effectiveTarget, checkCtx.TimeoutMs, checkCtx.Assertions)
	if err := w.Incident.HandleCheckResult(ctx, checkCtx, result); err != nil {
		w.Log.Printf("handle check result failed for check %d: %v", checkCtx.ID, err)
		return
	}

	// Record uptime based on the check result directly.
	// Any failed check (critical or not) counts as downtime in the uptime chart.
	uptimeStatus := "up"
	if !result.Healthy {
		uptimeStatus = "down"
	}
	if err := w.Store.RecordProjectUptimeStatus(ctx, checkCtx.ProjectID, uptimeStatus, time.Now().UTC()); err != nil {
		w.Log.Printf("record uptime failed for project %d: %v", checkCtx.ProjectID, err)
	}

	if result.Healthy {
		w.Log.Printf("check %d healthy (%dms)", checkCtx.ID, result.ResponseTimeMs)
	} else {
		w.Log.Printf("check %d failed: %s", checkCtx.ID, result.ErrorMessage)
	}
}

func (w *Worker) handleFixJob(ctx context.Context, job queue.FixJob) {
	fix, err := w.Store.GetProjectFix(ctx, job.ProjectID, job.FixID)
	if err != nil {
		w.Log.Printf("load fix failed: %v", err)
		_ = w.Store.InsertLog(ctx, job.ProjectID, "error", "manual fix lookup failed: "+err.Error())
		return
	}
	if fix == nil {
		w.Log.Printf("manual fix not found project=%d fix=%d", job.ProjectID, job.FixID)
		_ = w.Store.InsertLog(ctx, job.ProjectID, "error", fmt.Sprintf("manual fix %d not found", job.FixID))
		return
	}

	// Record fix run as "running"
	fixIDPtr := &fix.ID
	runID, insertErr := w.Store.InsertFixRun(ctx, job.ProjectID, fixIDPtr, fix.Name, fix.ScriptPath, "manual", job.RequestedBy)
	if insertErr != nil {
		w.Log.Printf("insert fix run failed: %v", insertErr)
	}

	_ = w.Store.InsertLog(ctx, job.ProjectID, "warn", fmt.Sprintf("manual fix triggered: %s by %s", fix.Name, job.RequestedBy))

	// Load per-project env vars for the fix script execution.
	envVars, envErr := w.Store.GetFixEnvVarsForExecution(ctx, job.ProjectID)
	if envErr != nil {
		w.Log.Printf("load fix env vars failed: %v", envErr)
		envVars = nil
	}

	started := time.Now()
	result, execErr := w.AutofixEngine.Execute(ctx, autofix.FixDefinition{
		Name:       fix.Name,
		ScriptPath: fix.ScriptPath,
		TimeoutSec: fix.TimeoutSec,
		EnvVars:    envVars,
	})
	durationMs := int(time.Since(started).Milliseconds())

	exitCode := 0
	if execErr != nil {
		exitCode = 1
		// Try to extract real exit code
		if exitErr, ok := execErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}

		w.Log.Printf("manual fix failed project=%d fix=%d: %v", job.ProjectID, job.FixID, execErr)
		_ = w.Store.InsertLog(ctx, job.ProjectID, "error", fmt.Sprintf("manual fix %s failed: %s", fix.Name, result.Output))

		if runID > 0 {
			_ = w.Store.UpdateFixRunResult(ctx, runID, false, exitCode, result.Output, durationMs)
		}
		return
	}

	w.Log.Printf("manual fix succeeded project=%d fix=%d", job.ProjectID, job.FixID)
	_ = w.Store.InsertLog(ctx, job.ProjectID, "warn", fmt.Sprintf("manual fix %s succeeded: %s", fix.Name, result.Output))

	if runID > 0 {
		_ = w.Store.UpdateFixRunResult(ctx, runID, true, 0, result.Output, durationMs)
	}
}

func (w *Worker) Validate() error {
	if w.Store == nil {
		return fmt.Errorf("worker store is nil")
	}
	if w.Queue == nil {
		return fmt.Errorf("worker queue is nil")
	}
	if w.AutofixEngine == nil {
		return fmt.Errorf("worker autofix engine is nil")
	}
	if w.Incident == nil {
		return fmt.Errorf("worker incident service is nil")
	}
	return nil
}

// buildEffectiveTarget constructs the final target by combining the project
// domain with the check's route. For HTTP checks the result is "domain/route"
// (the monitor layer prepends the scheme). For TCP and ping checks the domain
// itself is the target since path-based routes don't apply.
func buildEffectiveTarget(checkType, domain, route string) string {
	domain = strings.TrimSpace(domain)
	route = strings.TrimSpace(route)

	switch checkType {
	case "http":
		// If the route is already a full URL, use it as-is.
		if strings.HasPrefix(route, "http://") || strings.HasPrefix(route, "https://") {
			return route
		}
		if domain == "" {
			return route
		}
		// Ensure exactly one slash between domain and route.
		domain = strings.TrimRight(domain, "/")
		if route == "" || route == "/" {
			return domain + "/"
		}
		if !strings.HasPrefix(route, "/") {
			route = "/" + route
		}
		return domain + route
	default:
		// TCP and ping – the domain is the host; route is ignored.
		if domain != "" {
			return domain
		}
		return route
	}
}
