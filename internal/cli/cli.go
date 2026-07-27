// Package cli wires the snowball command tree onto the config/toolchain/render
// packages. Every render command loads snowball.yaml, then runs doctor before
// touching the toolchain so a missing dependency fails fast with a clear fix.
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/codcod/snowball/internal/config"
	"github.com/codcod/snowball/internal/render"
	"github.com/codcod/snowball/internal/toolchain"
)

var configPath string

// Execute runs the root command. version is injected from main.
func Execute(version string) error {
	root := &cobra.Command{
		Use:           "snowball",
		Short:         "Render AsciiDoc books to PDF/EPUB via the native asciidoctor toolchain",
		SilenceUsage:  true,
		SilenceErrors: false,
		Version:       version,
	}
	root.PersistentFlags().StringVarP(&configPath, "config", "c", "",
		"path to snowball.yaml (default: ./snowball.yaml)")

	root.AddCommand(buildCmd(), checkCmd(), doctorCmd(), setupCmd(), initCmd(), versionCmd(version))
	return root.Execute()
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

func buildCmd() *cobra.Command {
	var (
		pdf, epub bool
		outDir    string
		rev, date string
		books     []string
	)
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Render configured books to PDF and/or EPUB",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
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
			})
		},
	}
	cmd.Flags().BoolVar(&pdf, "pdf", false, "render PDF (default: config formats)")
	cmd.Flags().BoolVar(&epub, "epub", false, "render EPUB (default: config formats)")
	cmd.Flags().StringVarP(&outDir, "out", "o", "", "output directory (default: each book's dir)")
	cmd.Flags().StringVar(&rev, "rev", "", "revnumber override (default: git describe)")
	cmd.Flags().StringVar(&date, "date", "", "revdate override (default: today)")
	cmd.Flags().StringArrayVar(&books, "book", nil, "limit to book(s) by out/src name (repeatable)")
	return cmd
}

func checkCmd() *cobra.Command {
	var books []string
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Validate book masters (render + discard) — for MR pipelines",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			if err := requireToolchain(); err != nil {
				return err
			}
			return render.Check(cfg, render.Options{Books: books})
		},
	}
	cmd.Flags().StringArrayVar(&books, "book", nil, "limit to book(s) by out/src name (repeatable)")
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
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write a starter snowball.yaml in the current directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := os.Stat(config.DefaultFile); err == nil && !force {
				return fmt.Errorf("%s already exists (use --force to overwrite)", config.DefaultFile)
			}
			if err := os.WriteFile(config.DefaultFile, []byte(starterConfig), 0o644); err != nil {
				return err
			}
			fmt.Printf("snowball: wrote %s\n", config.DefaultFile)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing snowball.yaml")
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

const starterConfig = `books:
  - src: docs/user-manual.adoc
    out: users-manual
  - src: docs/developer-handbook.adoc
    out: developers-handbook
theme: docs/pdf-theme/ai-sdlc-theme.yml
attributes: docs/attributes.adoc
formats: [pdf, epub]
revision:
  from: git-describe
  date-format: "%d %B %Y"
mermaid:
  format: png
  puppeteer-args: ["--no-sandbox", "--disable-dev-shm-usage", "--disable-gpu"]
failure-level:
  pdf: WARN
  epub: ERROR
  check: WARN
`
