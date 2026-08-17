// Package controller reconciles Herdr tab labels against the live session.
//
// Reconciliation is snapshot-authoritative: every pass takes a fresh snapshot, derives tab
// numbers from the current order rather than from Herdr's native numbering, and compares
// the desired label against what the server currently reports. Nothing is cached between
// passes — not the label history, not the configuration, not the order.
package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/btj93/herdr-tabline/internal/collector"
	"github.com/btj93/herdr-tabline/internal/config"
	"github.com/btj93/herdr-tabline/internal/herdrapi"
	"github.com/btj93/herdr-tabline/internal/model"
	"github.com/btj93/herdr-tabline/internal/render"
)

// Trigger distinguishes a background refresh from an explicit user action.
type Trigger uint8

const (
	TriggerAuto Trigger = iota
	TriggerAction
)

func (t Trigger) String() string {
	if t == TriggerAction {
		return "action"
	}
	return "auto"
}

// ConfigSource supplies the current compiled configuration. *config.Manager implements it.
type ConfigSource interface {
	Current() (*config.Compiled, bool)
	ReloadIfChanged() (bool, error)
}

// StateStore persists label history. *state.Store implements it.
type StateStore interface {
	Load() (map[string]model.LabelHistory, error)
	Update(func(map[string]model.LabelHistory) bool) (bool, error)
}

type Options struct {
	Client herdrapi.Client
	Config ConfigSource
	Store  StateStore
	Now    func() time.Time
	Logger *slog.Logger
}

type Controller struct {
	client herdrapi.Client
	config ConfigSource
	store  StateStore
	now    func() time.Time
	log    *slog.Logger
}

func New(options Options) *Controller {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1}))
	}
	return &Controller{
		client: options.Client,
		config: options.Config,
		store:  options.Store,
		now:    now,
		log:    logger,
	}
}

// Reconcile brings tab labels in line with one fresh snapshot.
//
// Only a transport or configuration failure that invalidates the whole pass is returned.
// Per-tab failures — an unreachable pane, a template that will not execute, a refused
// rename — are logged and skipped so one bad tab cannot cost every other tab its refresh.
func (c *Controller) Reconcile(ctx context.Context, trigger Trigger, targetTabID string) error {
	if trigger == TriggerAction && targetTabID == "" {
		return errors.New("an action reconciliation requires a target tab id")
	}
	compiled, err := c.currentConfig()
	if err != nil {
		return err
	}

	history, err := c.store.Load()
	if err != nil {
		return fmt.Errorf("load label history: %w", err)
	}
	snapshot, err := c.client.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}

	// Context order is derived before any profile resolution, so Tab.Number reflects the
	// current position of each tab rather than the number Herdr happens to report.
	contexts := collector.Build(snapshot, history, c.now())
	engine := render.New(compiled.Aliases, compiled.Icons)

	updates := map[string]model.LabelHistory{}
	live := make(map[string]bool, len(contexts))
	for index := range contexts {
		tabContext := contexts[index]
		live[tabContext.Tab.ID] = true

		// A label the plugin did not write is the tab's new source label, and must be
		// recorded even when this tab is not eligible for a rename in this pass.
		if changed, entry := observeSourceLabel(history, tabContext); changed {
			updates[tabContext.Tab.ID] = entry
		}
		if trigger == TriggerAction && tabContext.Tab.ID != targetTabID {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		written, entry, err := c.reconcileTab(ctx, engine, compiled, trigger, tabContext, history)
		if err != nil {
			c.log.Warn("skipped tab", "tab", tabContext.Tab.ID, "trigger", trigger.String(), "error", err)
			continue
		}
		if written {
			updates[tabContext.Tab.ID] = entry
		}
	}

	if trigger == TriggerAction && !live[targetTabID] {
		return fmt.Errorf("target tab %q is not present in the current snapshot", targetTabID)
	}
	return c.commit(updates, live)
}

func (c *Controller) currentConfig() (*config.Compiled, error) {
	if _, err := c.config.ReloadIfChanged(); err != nil {
		// A bad edit must not stop the daemon: the manager retains the last valid value.
		c.log.Warn("configuration reload failed; continuing with the last valid configuration", "error", err)
	}
	compiled, ok := c.config.Current()
	if !ok {
		return nil, errors.New("no valid configuration is available")
	}
	return compiled, nil
}

// reconcileTab resolves, renders, and writes one tab. It reports whether a rename was
// actually performed, alongside the history entry that write implies.
func (c *Controller) reconcileTab(
	ctx context.Context,
	engine *render.Engine,
	compiled *config.Compiled,
	trigger Trigger,
	tabContext model.Context,
	history map[string]model.LabelHistory,
) (bool, model.LabelHistory, error) {
	// Resolve once to learn the mode, so an ineligible tab costs no process call.
	if !eligible(compiled.Resolve(tabContext).Mode, trigger) {
		return false, model.LabelHistory{}, nil
	}
	if tabContext.Pane.ID != "" {
		info, err := c.client.ProcessInfo(ctx, tabContext.Pane.ID)
		if err != nil {
			return false, model.LabelHistory{}, fmt.Errorf("process info for pane %s: %w", tabContext.Pane.ID, err)
		}
		collector.AttachProcess(&tabContext, info)
	}

	// Resolve again now that process fields are populated: matchers such as process_regex
	// can only be evaluated once the foreground process is known.
	effective := compiled.Resolve(tabContext)
	if !eligible(effective.Mode, trigger) {
		return false, model.LabelHistory{}, nil
	}
	tabContext.Profile = effective.ProfileName

	// Execute already refuses a template that renders nothing displayable, so an empty
	// label cannot reach the rename below; re-checking it here would be unreachable.
	desired, err := engine.Execute(effective.Template, tabContext, effective.MaxWidth)
	if err != nil {
		return false, model.LabelHistory{}, fmt.Errorf("render: %w", err)
	}

	// The only valid reason to skip is that the server already shows this label. Skipping
	// because it matches what the plugin wrote last time would let a manual rename persist.
	if desired == tabContext.Tab.CurrentLabel {
		return false, model.LabelHistory{}, nil
	}
	if err := c.client.RenameTab(ctx, tabContext.Tab.ID, desired); err != nil {
		return false, model.LabelHistory{}, fmt.Errorf("rename: %w", err)
	}

	entry := history[tabContext.Tab.ID]
	entry.SourceLabel = sourceLabelFor(history, tabContext)
	entry.RenderedLabel = desired
	return true, entry, nil
}

// observeSourceLabel reports a source label that changed outside the plugin. The collector
// already resolves Tab.Label to the last label the plugin did not write, so a difference
// from the stored value means the user renamed the tab.
func observeSourceLabel(history map[string]model.LabelHistory, tabContext model.Context) (bool, model.LabelHistory) {
	entry, known := history[tabContext.Tab.ID]
	observed := sourceLabelFor(history, tabContext)
	if known && entry.SourceLabel == observed {
		return false, entry
	}
	if !known && observed == "" {
		return false, entry
	}
	entry.SourceLabel = observed
	return true, entry
}

func sourceLabelFor(history map[string]model.LabelHistory, tabContext model.Context) string {
	if prior, ok := history[tabContext.Tab.ID]; ok && tabContext.Tab.CurrentLabel == prior.RenderedLabel {
		// The server is still showing the plugin's own label, so the source is unchanged.
		return prior.SourceLabel
	}
	return tabContext.Tab.CurrentLabel
}

func eligible(mode config.Mode, trigger Trigger) bool {
	switch mode {
	case config.ModeAuto:
		return true
	case config.ModeKeybind:
		return trigger == TriggerAction
	default:
		return false
	}
}

// commit merges this pass's touched entries and purges tabs that no longer exist, in one
// locked read-modify-write. An unchanged map performs no state-file write.
func (c *Controller) commit(updates map[string]model.LabelHistory, live map[string]bool) error {
	_, err := c.store.Update(func(tabs map[string]model.LabelHistory) bool {
		for id, entry := range updates {
			tabs[id] = entry
		}
		for id := range tabs {
			if !live[id] {
				delete(tabs, id)
			}
		}
		return true
	})
	if err != nil {
		return fmt.Errorf("persist label history: %w", err)
	}
	return nil
}
