// Package cli wires the snowball command tree onto the config/toolchain/render
// packages. Every render command loads snowball.yaml, then runs doctor before
// touching the toolchain so a missing dependency fails fast with a clear fix.
package cli

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/codcod/snowball/internal/config"
	"github.com/codcod/snowball/internal/render"
	"github.com/codcod/snowball/internal/scaffold"
	"github.com/codcod/snowball/internal/toolchain"
)

// globals holds the persistent flags. It is owned by Execute and passed to each
// command constructor, rather than living at package scope, so that constructing
// a command tree has no effect on any other tree in the same process.
type globals struct {
	configPath string
	quiet      bool
	verbose    bool
}

// Execute runs the root command. version is injected from main.
func Execute(version string) error {
	root, _ := newRoot(version)
	return root.Execute()
}

// newRoot builds the command tree and returns it alongside its flag state, so
// tests can drive a fresh, isolated tree.
func newRoot(version string) (*cobra.Command, *globals) {
	g := &globals{}
	root := &cobra.Command{
		Use:           "snowball",
		Short:         "Render AsciiDoc books to PDF/EPUB via the native asciidoctor toolchain",
		SilenceUsage:  true,
		SilenceErrors: false,
		Version:       version,
	}
	root.PersistentFlags().StringVarP(&g.configPath, "config", "c", "",
		"path to snowball.yaml (default: nearest one, searching upwards)")
	root.PersistentFlags().BoolVarP(&g.quiet, "quiet", "q", false,
		"suppress progress; tool output is still shown on failure")
	// Deliberately no -v shorthand: cobra assigns -v to --version when it is
	// free, and 0.1.x shipped that. Taking it for --verbose would silently
	// turn `snowball -v` from a version string into help text, still exiting 0.
	root.PersistentFlags().BoolVar(&g.verbose, "verbose", false,
		"log every command snowball runs")

	root.AddCommand(buildCmd(g), watchCmd(g), checkCmd(g), cleanCmd(g), doctorCmd(), setupCmd(), initCmd(), scaffoldCmd(), versionCmd(version))
	return root, g
}

func versionCmd(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the snowball version",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println(version)
			return nil
		},
	}
}

func buildCmd(g *globals) *cobra.Command {
	var (
		pdf, epub bool
		outDir    string
		rev, date string
		books     []string
		jobs      int
	)
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Render configured books to PDF and/or EPUB",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(g.configPath)
			if err != nil {
				return err
			}
			if err := requireToolchain(); err != nil {
				return err
			}
			var formats []string
			if pdf {
				formats = append(formats, "pdf")
			}
			if epub {
				formats = append(formats, "epub")
			}
			return render.Build(cfg, render.Options{
				Formats: formats, OutDir: outDir, Rev: rev, Date: date, Books: books,
				Jobs: jobs, Quiet: g.quiet, Verbose: g.verbose,
			})
		},
	}
	cmd.Flags().BoolVar(&pdf, "pdf", false, "render PDF (default: config formats)")
	cmd.Flags().BoolVar(&epub, "epub", false, "render EPUB (default: config formats)")
	cmd.Flags().StringVarP(&outDir, "out", "o", "", "output directory (default: each book's dir)")
	cmd.Flags().StringVar(&rev, "rev", "", "revnumber override (default: git describe)")
	cmd.Flags().StringVar(&date, "date", "", "revdate override (default: today)")
	cmd.Flags().StringArrayVar(&books, "book", nil, "limit to book(s) by out/src name (repeatable)")
	cmd.Flags().IntVarP(&jobs, "jobs", "j", 0, "books to render concurrently (default: up to 4; 1 is serial)")
	return cmd
}

func checkCmd(g *globals) *cobra.Command {
	var books []string
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Validate book masters (render + discard) — for MR pipelines",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(g.configPath)
			if err != nil {
				return err
			}
			if err := requireToolchain(); err != nil {
				return err
			}
			return render.Check(cfg, render.Options{
				Books: books, Quiet: g.quiet, Verbose: g.verbose,
			})
		},
	}
	cmd.Flags().StringArrayVar(&books, "book", nil, "limit to book(s) by out/src name (repeatable)")
	return cmd
}

func watchCmd(g *globals) *cobra.Command {
	var (
		pdf, epub bool
		outDir    string
		rev, date string
		books     []string
		jobs      int
	)
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Render on every change to a book source, until interrupted",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(g.configPath)
			if err != nil {
				return err
			}
			// Checked once here rather than per rebuild.
			if err := requireToolchain(); err != nil {
				return err
			}
			var formats []string
			if pdf {
				formats = append(formats, "pdf")
			}
			if epub {
				formats = append(formats, "epub")
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return render.Watch(ctx, cfg, render.Options{
				Formats: formats, OutDir: outDir, Rev: rev, Date: date, Books: books,
				Jobs: jobs, Quiet: g.quiet, Verbose: g.verbose,
			})
		},
	}
	cmd.Flags().BoolVar(&pdf, "pdf", false, "render PDF (default: config formats)")
	cmd.Flags().BoolVar(&epub, "epub", false, "render EPUB (default: config formats)")
	cmd.Flags().StringVarP(&outDir, "out", "o", "", "output directory (default: each book's dir)")
	cmd.Flags().StringVar(&rev, "rev", "", "revnumber override (default: git describe)")
	cmd.Flags().StringVar(&date, "date", "", "revdate override (default: today)")
	cmd.Flags().StringArrayVar(&books, "book", nil, "limit to book(s) by out/src name (repeatable)")
	cmd.Flags().IntVarP(&jobs, "jobs", "j", 0, "books to render concurrently (default: up to 4; 1 is serial)")
	return cmd
}

func cleanCmd(g *globals) *cobra.Command {
	var (
		pdf, epub bool
		outDir    string
		books     []string
		withCache bool
	)
	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Remove the PDFs/EPUBs a build would produce",
		// No requireToolchain: removing files must not need ruby installed.
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(g.configPath)
			if err != nil {
				return err
			}
			var formats []string
			if pdf {
				formats = append(formats, "pdf")
			}
			if epub {
				formats = append(formats, "epub")
			}
			return render.Clean(cfg, render.Options{
				Formats: formats, OutDir: outDir, Books: books,
				Quiet: g.quiet, Verbose: g.verbose,
			}, withCache)
		},
	}
	cmd.Flags().BoolVar(&pdf, "pdf", false, "only remove PDFs (default: config formats)")
	cmd.Flags().BoolVar(&epub, "epub", false, "only remove EPUBs (default: config formats)")
	cmd.Flags().StringVarP(&outDir, "out", "o", "", "output directory the build used")
	cmd.Flags().StringArrayVar(&books, "book", nil, "limit to book(s) by out/src name (repeatable)")
	cmd.Flags().BoolVar(&withCache, "cache", false, "also remove asciidoctor's .asciidoctor cache directories")
	return cmd
}

func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Verify the native toolchain is installed",
		RunE: func(cmd *cobra.Command, args []string) error {
			reports, ok := toolchain.Doctor()
			toolchain.PrintReport(os.Stdout, reports)
			if !ok {
				return fmt.Errorf("toolchain incomplete — run `snowball setup`")
			}
			fmt.Println("snowball: toolchain ok")
			return nil
		},
	}
}

func setupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Install the pinned toolchain (gems + mermaid-cli + Chrome)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return toolchain.Setup()
		},
	}
}

func initCmd() *cobra.Command {
	var force bool
	var projectName string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write a starter snowball.yaml in the current directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := os.Stat(config.DefaultFile); err == nil && !force {
				return fmt.Errorf("%s already exists (use --force to overwrite)", config.DefaultFile)
			}
			name := projectName
			if name == "" {
				if wd, err := os.Getwd(); err == nil {
					name = filepath.Base(wd)
				}
			}
			if err := os.WriteFile(config.DefaultFile, scaffold.StarterConfig(name, false), 0o644); err != nil {
				return err
			}
			fmt.Printf("snowball: wrote %s\n", config.DefaultFile)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing snowball.yaml")
	cmd.Flags().StringVar(&projectName, "project-name", "", "name substituted into the starter config (default: the current directory's name)")
	return cmd
}

func scaffoldCmd() *cobra.Command {
	var (
		projectName string
		force       bool
		dryRun      bool
		noWorkflow  bool
	)
	cmd := &cobra.Command{
		Use:   "scaffold",
		Short: "Lay down a starter AsciiDoc docs skeleton, snowball.yaml, justfile recipes and a GitHub release-attach workflow",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := os.Getwd()
			if err != nil {
				return err
			}
			res, err := scaffold.Docs(root, scaffold.Options{
				ProjectName: projectName,
				Force:       force,
				DryRun:      dryRun,
				NoWorkflow:  noWorkflow,
			})
			for _, c := range res.Created {
				fmt.Printf("  + %s\n", c)
			}
			for _, s := range res.Skipped {
				fmt.Printf("  = %s\n", s)
			}
			for _, n := range res.Notes {
				fmt.Printf("\n%s\n", n)
			}
			return err
		},
	}
	cmd.Flags().StringVar(&projectName, "project-name", "", "name substituted into the scaffolded docs (default: the current directory's name)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite files that already exist")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "list what would be created/changed, changing nothing")
	cmd.Flags().BoolVar(&noWorkflow, "no-workflow", false, "skip writing the GitHub release-attach workflow")
	return cmd
}

// requireToolchain runs doctor before a render so a missing dependency fails fast.
func requireToolchain() error {
	reports, ok := toolchain.Doctor()
	if !ok {
		toolchain.PrintReport(os.Stdout, reports)
		return fmt.Errorf("toolchain incomplete — run `snowball setup`")
	}
	return nil
}
