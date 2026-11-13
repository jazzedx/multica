package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/daemon"
)

var updateDownloadTimeout time.Duration = cli.DefaultUpdateDownloadTimeout

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update multica to the latest version",
	RunE:  runUpdate,
}

func init() {
	updateCmd.Flags().DurationVar(&updateDownloadTimeout, "download-timeout", cli.DefaultUpdateDownloadTimeout, "Maximum time to wait for the release archive download")
}

func runUpdate(_ *cobra.Command, _ []string) error {
	if updateDownloadTimeout <= 0 {
		return fmt.Errorf("download timeout must be greater than zero")
	}

	fmt.Fprintf(os.Stderr, "Current version: %s (commit: %s, built: %s)\n", version, commit, date)

	// A self-hosted deployment does not pull the official release from GitHub:
	// that would overwrite this customized CLI.
	if cfg, err := cli.LoadCLIConfig(); err == nil && cfg.Token != "" && cfg.ServerURL != "" && !daemon.IsOfficialCloudServer(cfg.ServerURL) {
		return updateFromSelfHost(cfg.ServerURL, cfg.Token)
	}

	// Check the configured release source.
	latest, err := cli.FetchLatestRelease()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not check latest version: %v\n", err)
	} else {
		latestVer := strings.TrimPrefix(latest.TagName, "v")
		currentVer := strings.TrimPrefix(version, "v")
		if currentVer == latestVer {
			fmt.Fprintln(os.Stderr, "Already up to date.")
			return nil
		}
		fmt.Fprintf(os.Stderr, "Latest version:  %s\n\n", latest.TagName)
	}

	// Detect installation method and update accordingly.
	if cli.IsBrewInstall() {
		fmt.Fprintln(os.Stderr, "Updating via Homebrew...")
		output, err := cli.UpdateViaBrew()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s\n", output)
			return fmt.Errorf("brew upgrade failed: %w\nYou can try manually: brew upgrade multica-ai/tap/multica", err)
		}
		fmt.Fprintln(os.Stderr, "Update complete.")
		return nil
	}

	// Not installed via brew — download the binary from the configured release source.
	if latest == nil {
		return fmt.Errorf("could not determine latest version; check the configured release source")
	}
	targetVersion := latest.TagName
	fmt.Fprintf(os.Stderr, "Downloading %s from the configured release source...\n", targetVersion)
	output, err := cli.UpdateViaDownloadWithTimeout(targetVersion, updateDownloadTimeout)
	if err != nil {
		return fmt.Errorf("update failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "%s\nUpdate complete.\n", output)
	return nil
}

func updateFromSelfHost(serverURL, token string) error {
	fmt.Fprintf(os.Stderr, "Checking the CLI published by %s...\n", serverURL)
	rel, err := cli.FetchServerCLIMetadata(context.Background(), serverURL, token)
	if err != nil {
		return fmt.Errorf("update from self-hosted server failed: %w", err)
	}
	if rel.Commit == commit {
		fmt.Fprintln(os.Stderr, "Already up to date.")
		return nil
	}
	archive, err := cli.DownloadServerCLIArchive(context.Background(), serverURL, token)
	if err != nil {
		return fmt.Errorf("update from self-hosted server failed: %w", err)
	}
	updated, err := cli.ApplyServerCLI(commit, rel, archive)
	if err != nil {
		return fmt.Errorf("update from self-hosted server failed: %w", err)
	}
	if !updated {
		fmt.Fprintln(os.Stderr, "Already up to date.")
		return nil
	}
	fmt.Fprintf(os.Stderr, "Updated to %s (%s).\n", rel.Version, rel.Commit)
	return nil
}
