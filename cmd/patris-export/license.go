package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/atomicdeploy/patris-export/pkg/licensing"
	"github.com/spf13/cobra"
)

const licenseBypassAnnotation = "patris-export/license-management"

func newLicenseCommand() *cobra.Command {
	licenseCmd := &cobra.Command{
		Use:         "license",
		Short:       "Inspect or manage the optional machine license",
		Annotations: map[string]string{licenseBypassAnnotation: "true"},
	}

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Print the current license status as JSON",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return printLicenseStatus(cmd, licensing.CurrentStatus(cmd.Context()))
		},
	}
	challengeCmd := &cobra.Command{
		Use:   "challenge",
		Short: "Print this machine's license challenge",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			challenge, err := licensing.Challenge(cmd.Context())
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), challenge)
			return err
		},
	}
	installCmd := &cobra.Command{
		Use:   "install [license-key]",
		Short: "Validate and install a per-user license key",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, err := licenseKeyInput(cmd, args)
			if err != nil {
				return err
			}
			status, err := licensing.Install(cmd.Context(), key)
			if err != nil {
				return err
			}
			return printLicenseStatus(cmd, status)
		},
	}
	installCmd.Flags().String("file", "", "Read the license key from a UTF-8 text file")
	removeCmd := &cobra.Command{
		Use:   "remove",
		Short: "Remove the per-user license key",
		Long:  "Remove the per-user license key. A legacy key beside the executable is discovery-only and is never removed.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			status, err := licensing.Remove(cmd.Context())
			if err != nil {
				return err
			}
			return printLicenseStatus(cmd, status)
		},
	}

	licenseCmd.AddCommand(statusCmd, challengeCmd, installCmd, removeCmd)
	return licenseCmd
}

func licenseCommandBypassesEnforcement(cmd *cobra.Command) bool {
	for current := cmd; current != nil; current = current.Parent() {
		if current.Annotations[licenseBypassAnnotation] == "true" {
			return true
		}
	}
	return false
}

func licenseKeyInput(cmd *cobra.Command, args []string) (string, error) {
	path, err := cmd.Flags().GetString("file")
	if err != nil {
		return "", err
	}
	if len(args) == 1 && strings.TrimSpace(path) != "" {
		return "", fmt.Errorf("provide either [license-key] or --file, not both")
	}
	if len(args) == 1 {
		return args[0], nil
	}
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("a license key or --file is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read license key file: %w", err)
	}
	return string(data), nil
}

func printLicenseStatus(cmd *cobra.Command, status licensing.Status) error {
	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return fmt.Errorf("encode license status: %w", err)
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), string(data))
	return err
}
