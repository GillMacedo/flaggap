// Command worker is a made-up queue worker for the flaggap examples. It
// reads its flags with spf13/cobra and spf13/pflag, the way most
// Kubernetes-style Go programs do.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func main() {
	var logLevel string
	root := &cobra.Command{Use: "worker", SilenceUsage: true}
	root.PersistentFlags().StringVar(&logLevel, "log-level", "info", "debug, info, warn or error")

	var (
		queue       string
		concurrency int
		poll        time.Duration
		dryRun      bool
		metrics     string
		threads     int
	)
	run := &cobra.Command{
		Use:   "run",
		Short: "Process jobs from a queue",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("run queue=%s concurrency=%d poll-interval=%s dry-run=%t metrics-bind-address=%s log-level=%s args=%q\n",
				queue, concurrency, poll, dryRun, metrics, logLevel, args)
			return nil
		},
	}
	f := run.Flags()
	f.StringVarP(&queue, "queue", "q", "default", "queue to read jobs from")
	f.IntVarP(&concurrency, "concurrency", "c", 4, "jobs to run at once")
	f.DurationVar(&poll, "poll-interval", 5*time.Second, "how often to poll an empty queue")
	f.BoolVar(&dryRun, "dry-run", false, "log jobs without running them")
	f.StringVar(&metrics, "metrics-bind-address", ":8080", "address to serve metrics on")
	f.IntVar(&threads, "threads", 0, "old name of --concurrency")
	f.MarkDeprecated("threads", "use --concurrency instead")

	// klog-style verbosity from the standard flag package, as many
	// Kubernetes programs add it.
	goflags := flag.NewFlagSet("go", flag.ContinueOnError)
	goflags.Int("v", 0, "log verbosity")
	root.PersistentFlags().AddGoFlagSet(goflags)
	root.SetGlobalNormalizationFunc(func(_ *pflag.FlagSet, name string) pflag.NormalizedName {
		return pflag.NormalizedName(strings.ReplaceAll(name, "_", "-"))
	})

	root.AddCommand(run)
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
