package cmd

import (
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"steel/internal/inventory"
	"steel/internal/playbook"
	"steel/internal/runner/probe"
)

var pingTimeout time.Duration

// pingCmd probes every host in the entry file's embedded
// inventory. The probe is a real SSH handshake followed by an
// `echo pong` exec — stronger than a plain TCP-port check, and
// the same one `steel apply` runs as a preflight before
// dispatching the playbook.
var pingCmd = &cobra.Command{
	Use:   "ping <playbook> [target...]",
	Short: "Probe SSH connectivity of hosts in the inventory by running `echo pong`",
	Long: "ping loads the entry playbook, opens an SSH session on each " +
		"target host, and runs `echo pong` to verify the host is reachable, " +
		"accepts the configured credentials, and can execute commands. " +
		"This is a stronger check than a plain TCP-port probe — `netcat " +
		"host:22` passes even when the SSH daemon is wedged or the auth " +
		"path is broken.\n\n" +
		"The host list is read from the entry file's top-level " +
		"`hosts:` block (and any sibling group keys). Targets may be " +
		"host names or group names — group names are expanded by the " +
		"inventory's resolver, so group-of-groups work transparently.\n\n" +
		"With no TARGET argument, every host in the inventory is probed. " +
		"Hosts are probed concurrently. Exit code is 0 if all targets are " +
		"reachable, 1 if any failed.",
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		playbookPath := args[0]

		// Load the entry so we can pick up its embedded
		// hosts:/groups: block. We deliberately do NOT use
		// the plays — ping only cares about the inventory,
		// not the play list — but LoadEntry gives us both
		// in one file read, and the runlist expansion
		// validates the entry file as a side effect.
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

		targets := resolveTargets(inv, args[1:])
		if len(targets) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "no targets to probe")
			return nil
		}

		results := probe.All(targets, pingTimeout)
		probe.RenderTable(cmd.OutOrStdout(), results)

		if !probe.AllOK(results) {
			failed := 0
			for _, r := range results {
				if !r.OK {
					failed++
				}
			}
			return fmt.Errorf("%d/%d host(s) unreachable", failed, len(results))
		}
		fmt.Fprintf(cmd.OutOrStdout(), "\n%d/%d reachable\n", len(results), len(results))
		return nil
	},
}

// resolveTargets turns CLI args into a de-duplicated, sorted
// host list. Unknown targets are reported on stderr and skipped
// (so a single typo doesn't abort the rest of the probe).
func resolveTargets(inv *inventory.Inventory, args []string) []*inventory.Host {
	if len(args) == 0 {
		return inv.All()
	}
	seen := map[string]bool{}
	out := []*inventory.Host{}
	for _, t := range args {
		hs, err := inv.Hosts(t)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip %q: %v\n", t, err)
			continue
		}
		for _, h := range hs {
			if !seen[h.Name] {
				seen[h.Name] = true
				out = append(out, h)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func init() {
	pingCmd.Flags().DurationVar(&pingTimeout, "timeout", 3*time.Second, "per-host SSH dial timeout")
	rootCmd.AddCommand(pingCmd)
}
