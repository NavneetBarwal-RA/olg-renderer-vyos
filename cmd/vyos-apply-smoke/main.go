package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/routerarchitects/olg-renderer-vyos/apply"
)

const smokePrefix = "[smoke]"

var defaultRequiredBinaries = []string{
	"/opt/vyatta/sbin/vyatta-cfg-cmd-wrapper",
}

type smokeConfig struct {
	confirmed  bool
	target     string
	configUUID string
	mode       string
	save       bool
	skipApply  bool
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, time.Now))
}

func run(args []string, out io.Writer, now func() time.Time) int {
	cfg, usage, err := parseFlags(args, out, now)
	if err != nil {
		logf(out, "error=%v", err)
		usage()
		return 2
	}
	if err := validateConfirmationFlag(cfg.confirmed); err != nil {
		logf(out, "error=%v", err)
		usage()
		return 2
	}

	logf(out, "starting VyOS apply smoke test")
	logf(out, "warning: this modifies VyOS runtime configuration")
	logf(out, "warning: normal apply policy may delete interfaces bridge and nat source")

	if !cfg.skipApply {
		logf(out, "checking required binaries")
		requiredBinaries := requiredBinariesForSmoke(cfg.save)
		if err := checkRequiredBinaries(requiredBinaries); err != nil {
			logf(out, "binary check failed: %v", err)
			logCleanupGuidance(out)
			return 1
		}
		for _, path := range requiredBinaries {
			logf(out, "found %s", path)
		}
	} else {
		logf(out, "skip_apply=true; skipping required binary checks and Apply")
	}

	commands, err := buildSmokeCommands(cfg.mode)
	if err != nil {
		logf(out, "error=%v", err)
		return 2
	}
	desiredCommands := strings.Join(commands, "\n") + "\n"
	input := apply.Input{
		Target:          cfg.target,
		ConfigUUID:      cfg.configUUID,
		DesiredCommands: desiredCommands,
	}

	logf(out, "building apply input")
	logf(out, "target=%s config_uuid=%s mode=%s save=%t skip_apply=%t", cfg.target, cfg.configUUID, cfg.mode, cfg.save, cfg.skipApply)
	logf(out, "desired commands:")
	for _, command := range commands {
		fmt.Fprintf(out, "%s\n", command)
	}

	logf(out, "creating apply engine")
	options := []apply.Option{}
	if cfg.save {
		options = append(options, apply.WithSaveAfterCommit(true))
	}
	applier, err := apply.New(options...)
	if err != nil {
		logf(out, "error=%v", err)
		return 1
	}

	logf(out, "previewing plan with Prepare")
	plan, err := applier.Prepare(context.Background(), input)
	if err != nil {
		logf(out, "prepare failed")
		logf(out, "error=%v", err)
		logf(out, "code=%s", apply.CodeOf(err))
		return 1
	}
	logPlan(out, plan)

	if cfg.skipApply {
		logf(out, "completed preview without Apply")
		return 0
	}

	logf(out, "applying plan through Apply")
	result, err := applier.Apply(context.Background(), input)
	if err != nil {
		logf(out, "apply failed")
		logf(out, "error=%v", err)
		logf(out, "code=%s", apply.CodeOf(err))
		logResult(out, result)
		logCleanupGuidance(out)
		return 1
	}

	logResult(out, result)
	logf(out, "completed successfully")
	logCleanupGuidance(out)
	return 0
}

func requiredBinariesForSmoke(save bool) []string {
	return append([]string(nil), defaultRequiredBinaries...)
}

func parseFlags(args []string, out io.Writer, now func() time.Time) (smokeConfig, func(), error) {
	cfg := smokeConfig{}
	fs := flag.NewFlagSet("vyos-apply-smoke", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.BoolVar(&cfg.confirmed, "i-understand-this-modifies-vyos", false, "required safety confirmation for lab VyOS modification")
	fs.StringVar(&cfg.target, "target", "vyos", "apply target")
	fs.StringVar(&cfg.configUUID, "config-uuid", defaultConfigUUID(now), "config UUID for smoke traceability")
	fs.StringVar(&cfg.mode, "mode", "minimal", "smoke payload mode: minimal, bridge, or nat")
	fs.BoolVar(&cfg.save, "save", false, "save configuration after successful commit")
	fs.BoolVar(&cfg.skipApply, "skip-apply", false, "preview plan only; do not check VyOS binaries or call Apply")
	usage := fs.Usage
	if err := fs.Parse(args); err != nil {
		return cfg, usage, err
	}
	return cfg, usage, nil
}

func defaultConfigUUID(now func() time.Time) string {
	if now == nil {
		now = time.Now
	}
	return "smoke-" + now().UTC().Format("20060102T150405Z")
}

func validateConfirmationFlag(confirmed bool) error {
	if !confirmed {
		return errors.New("missing required --i-understand-this-modifies-vyos")
	}
	return nil
}

func buildSmokeCommands(mode string) ([]string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "minimal", "bridge":
		return []string{"set interfaces bridge br0 description 'OLG_APPLY_SMOKE_TEST'"}, nil
	case "nat":
		return []string{"set nat source rule 9999 translation address masquerade"}, nil
	default:
		return nil, fmt.Errorf("unsupported mode %q; expected minimal, bridge, or nat", mode)
	}
}

func checkRequiredBinaries(paths []string) error {
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if info.IsDir() {
			return fmt.Errorf("%s is a directory, not an executable", path)
		}
		if info.Mode()&0111 == 0 {
			return fmt.Errorf("%s is not executable", path)
		}
	}
	return nil
}

func logPlan(out io.Writer, plan apply.Plan) {
	logf(out, "plan delete_count=%d set_count=%d commit=%t save=%t", len(plan.DeleteCommands), len(plan.SetCommands), plan.Commit, plan.Save)
	for i, command := range plan.DeleteCommands {
		logf(out, "delete[%d]=%s", i, command)
	}
	for i, command := range plan.SetCommands {
		logf(out, "set[%d]=%s", i, command)
	}
}

func logResult(out io.Writer, result apply.Result) {
	logf(out, "result applied=%t saved=%t", result.Applied, result.Saved)
	logf(out, "commit_output=%s", trimForLog(result.CommitOutput))
	logf(out, "save_output=%s", trimForLog(result.SaveOutput))
	logf(out, "discard_output=%s", trimForLog(result.DiscardOutput))
}

func logCleanupGuidance(out io.Writer) {
	logf(out, "cleanup guidance:")
	logf(out, "the smoke test uses normal apply policy and may delete interfaces bridge and nat source")
	logf(out, "run only on a disposable/lab VyOS VM or restore config from backup afterward")
	logf(out, "preferred rollback is re-applying known-good desired config through the normal NATS agent path")
}

func logf(out io.Writer, format string, args ...any) {
	fmt.Fprintf(out, "%s %s\n", smokePrefix, fmt.Sprintf(format, args...))
}

func trimForLog(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "<empty>"
	}
	return trimmed
}
