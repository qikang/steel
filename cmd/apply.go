package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"steel/internal/inventory"
	"steel/internal/playbook"
	"steel/internal/runner"
	"steel/internal/runner/probe"
)

var (
	// applyProbeTimeout is the per-host SSH dial deadline for
	// the preflight connectivity check. Mirrors `steel ping`'s
	// --timeout default of 3s.
	applyProbeTimeout = 3 * time.Second
	// applyProbeSkip bypasses the preflight (skips the
	// connectivity check entirely). Useful for CI / scripted
	// runs where the operator has already validated
	// connectivity via `steel ping` and doesn't want a
	// second round of SSH dials before the apply.
	applyProbeSkip bool
)

var applyCmd = &cobra.Command{
	Use:   "apply <playbook>",
	Short: "Apply a playbook to hosts from an inventory",
	Long: "apply loads the playbook YAML, prints four preflight " +
		"conditions as informational hints (data disk mount, DNS, " +
		"timezone, OS package mirror) and asks the operator for a " +
		"single Y/N to " +
		"proceed, runs a quick SSH connectivity probe against every " +
		"host, and then dispatches each play to its target hosts. " +
		"Plays run sequentially; within a play, hosts run " +
		"concurrently by default. A play can opt into per-host " +
		"serial dispatch by setting `execution_mode: serial` at the " +
		"top level — useful for ordered bootstrap steps that must " +
		"not race each other. The first host failure stops the run " +
		"(Ansible-style fail-fast).\n\n" +
		"The host list is read from the entry file's top-level " +
		"`hosts:` block (and any sibling group keys). The preflight " +
		"prompt and the connectivity probe are non-blocking: the " +
		"operator may answer N to abort, or skip the probe entirely " +
		"via --skip-probe when the connectivity has already been " +
		"validated by a prior `steel ping`.\n\n" +
		"shell output is streamed to a single run-wide log file at " +
		"logs/<run-ts>/run.log; every play and every host appends to " +
		"the same file in execution order, with `host=` and `play=` " +
		"markers preserving the per-line context.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		playbookPath := args[0]

		entry, err := playbook.LoadEntry(playbookPath)
		if err != nil {
			return err
		}

		// The inventory MUST be embedded in the entry file —
		// there is no -i / --inventory flag.
		if len(entry.Hosts) == 0 && len(entry.Groups) == 0 {
			return fmt.Errorf("no inventory: %s has no top-level hosts: block", playbookPath)
		}
		inv, err := inventory.New(&inventory.File{Hosts: entry.Hosts, Groups: entry.Groups})
		if err != nil {
			return fmt.Errorf("playbook %q hosts: %w", playbookPath, err)
		}

		// Single-question preflight: print the four
		// conditions the operator must have already verified
		// before the apply runs (data disk mount, DNS,
		// timezone, OS package mirror), then ask for a
		// single Y/N to proceed. This is a non-blocking
		// confirmation — the binary does not actually inspect
		// the hosts; the four hints are reminders, not
		// questions the binary acts on. The operator types Y
		// (or hits Enter) to proceed, N to abort, anything
		// else is invalid.
		if !confirmPreflight(cmd, inv) {
			fmt.Fprintln(cmd.ErrOrStderr(), "apply aborted by operator")
			return fmt.Errorf("preflight not confirmed")
		}

		// Connectivity probe: SSH into every host and run
		// `echo pong` to catch auth / network issues before
		// the play dispatch. --skip-probe bypasses this for
		// CI / scripted runs.
		if !applyProbeSkip {
			fmt.Fprintln(cmd.OutOrStdout(), "running SSH connectivity probe...")
			results := probe.All(inv.All(), applyProbeTimeout)
			probe.RenderTable(cmd.OutOrStdout(), results)
			if !probe.AllOK(results) {
				failed := 0
				for _, r := range results {
					if !r.OK {
						failed++
					}
				}
				return fmt.Errorf("connectivity probe: %d/%d host(s) unreachable", failed, len(results))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%d/%d reachable\n\n", len(results), len(results))
		}

		// Per-run log root: <cwd>/logs/<UTC-timestamp>. A
		// single run.log file lives inside this directory,
		// holding every play and every host in execution
		// order. Using a timestamp per run keeps previous
		// runs intact for inspection.
		logRoot := filepath.Join("logs", time.Now().UTC().Format("20060102-150405"))
		if err := os.MkdirAll(logRoot, 0o755); err != nil {
			return fmt.Errorf("create log dir %q: %w", logRoot, err)
		}

		return runner.Apply(inv, entry.Plays, entry.Vars, logRoot)
	},
}

// preflightHints are the four operator-side conditions the
// binary prints before any SSH probe or play dispatch. The
// binary does NOT actually inspect the hosts — these are pure
// hints for the operator to have already verified before
// running apply (mount of /data, DNS resolv.conf, timezone,
// OS package mirror). Answering the single Y/N below is the
// only gate.
var preflightHints = []string{
	"所有 k8s 节点 /data 目录和 data 磁盘正确挂载了吗？",
	"所有 k8s 节点 /etc/resolv.conf DNS 解析都正常设置了吗？",
	"所有 k8s 节点时区设置正确了吗？",
	"所有 k8s 节点 OS 软件源设置正确了吗？",
}

// confirmPreflight prints the four preflight conditions as
// informational hints and asks the operator for a SINGLE Y/N
// to proceed. Returns true when the operator answers Y (or
// hits Enter, the default-yes). Anything other than a single
// Y/N is treated as an invalid answer and aborts the apply —
// the operator should re-run and type a clean `y` or `n`.
//
// Accepted input shapes (all case-insensitive, surrounding
// whitespace ignored):
//
//	"" / "y" / "yes"   → proceed
//	"n" / "no"         → abort
//	anything else      → invalid; abort
//
// This is intentionally a single-question prompt, not a
// per-question checklist: the four hints are not questions
// the binary is willing to act on, they're a reminder. A
// per-question "Y Y Y" loop just adds typing and an extra
// place to mis-key without any real check behind it.
//
// Reads from cmd.InOrStdin() so cobra's test harness can
// inject a fake reader; falls back to os.Stdin if the cobra
// command has no stdin override.
func confirmPreflight(cmd *cobra.Command, inv *inventory.Inventory) bool {
	out := cmd.OutOrStdout()

	// 第一段:组信息。展示顺序硬编码为
	//   masters → workers → allnode → addworkers
	// 这是 K8s 安装场景下操作员最关心的"控制面 / 工作节点 / 全集群 / 扩容节点"
	// 四分类,字典序(addworkers < allnode < masters < workers)对运维反直觉。
	// 没有该组时自动跳过(比如 single-master 部署没 workers / addworkers 就不显示)。
	// 其他自定义组按字典序追加在末尾,防止 K8s 之外的场景漏展示。
	fmt.Fprintln(out, "配置文件已读取到以下组信息：")
	groupDisplayOrder := []string{"masters", "workers", "allnode", "addworkers"}
	rendered := map[string]bool{}
	idx := 0
	for _, g := range groupDisplayOrder {
		hs, _ := inv.Hosts(g)
		if len(hs) == 0 {
			continue
		}
		idx++
		fmt.Fprintf(out, "  %d. group %q: %d host\n", idx, g, len(hs))
		rendered[g] = true
	}
	var extras []string
	for _, g := range inv.GroupNames() {
		if !rendered[g] {
			extras = append(extras, g)
		}
	}
	sort.Strings(extras)
	for _, g := range extras {
		hs, _ := inv.Hosts(g)
		if len(hs) == 0 {
			continue
		}
		idx++
		fmt.Fprintf(out, "  %d. group %q: %d host\n", idx, g, len(hs))
	}

	// 第二段:k8s 节点信息确认。binary 不实际校验,纯展示提醒。
	fmt.Fprintln(out, "k8s节点信息确认：")
	for i, q := range preflightHints {
		fmt.Fprintf(out, "  %d. %s\n", i+1, q)
	}

	// 第三段:Y/N 询问。readYesNo 内部已经 case-insensitive,
	// 大写 Y / 小写 y / 大写 N / 小写 n 都识别,空行默认 Y。
	in := cmd.InOrStdin()
	if in == nil {
		in = os.Stdin
	}
	rdr := bufio.NewReader(in)
	fmt.Fprint(out, "是否继续? [Y/N] ")
	ok, valid := readYesNo(rdr)
	if !valid {
		fmt.Fprintln(cmd.ErrOrStderr(), "\npreflight: please answer Y or N (Enter defaults to Y)")
		return false
	}
	if !ok {
		fmt.Fprintln(cmd.ErrOrStderr(), "\npreflight: declined, aborting")
		return false
	}
	fmt.Fprintln(out)
	return true
}

// readYesNo reads one line of input and returns (answer, valid).
// `valid` is false on any input that is not "", "y"/"yes",
// or "n"/"no" (case-insensitive, surrounding whitespace
// ignored). An empty line defaults to yes. EOF on a non-empty
// partial line (e.g. the operator typed `y` with no trailing
// newline) is treated as a valid answer — we got the bytes,
// just without a terminator. EOF on an empty stream (or any
// other read error) is treated as invalid so the apply
// doesn't proceed without a confirmation.
func readYesNo(rdr *bufio.Reader) (bool, bool) {
	line, err := rdr.ReadString('\n')
	if err != nil {
		// Distinguish a clean partial-line read (the operator
		// typed `y` and the stream ended) from a truly empty
		// stream. The former still constitutes a valid
		// answer; the latter is "no input at all" and should
		// abort the apply.
		if len(strings.TrimSpace(line)) == 0 {
			return false, false
		}
	}
	s := strings.TrimSpace(strings.ToLower(line))
	s = strings.Trim(s, `"'`)
	switch s {
	case "", "y", "yes":
		return true, true
	case "n", "no":
		return false, true
	default:
		return false, false
	}
}

func init() {
	applyCmd.Flags().DurationVar(&applyProbeTimeout, "probe-timeout", 3*time.Second, "per-host SSH dial timeout for the preflight connectivity check")
	applyCmd.Flags().BoolVar(&applyProbeSkip, "skip-probe", false, "skip the preflight SSH connectivity probe (use when a prior 'steel ping' has already validated connectivity)")
	rootCmd.AddCommand(applyCmd)
}
