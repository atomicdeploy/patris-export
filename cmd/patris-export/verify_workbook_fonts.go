package main

import (
	"fmt"

	"github.com/atomicdeploy/patris-export/pkg/recordsink"
	"github.com/spf13/cobra"
)

func newVerifyWorkbookFontsCommand() *cobra.Command {
	return &cobra.Command{
		Use:           "verify-workbook-fonts <calculator.xlsx|xlsm|xltm>",
		Short:         "Hard-validate the fixed calculator font role map",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			policy, err := recordsink.ReadDynamicWorkbookFontPolicy(args[0])
			if err != nil {
				return fmt.Errorf("workbook font verification failed: %w", err)
			}
			report, err := recordsink.ValidateDynamicWorkbookFontPolicy(args[0], policy)
			if err != nil {
				return fmt.Errorf("workbook font verification failed: %w", err)
			}
			_, err = fmt.Fprintf(
				cmd.OutOrStdout(),
				"valid workbook fonts: persian=%q latin=%q mapped_cells=%d drawing_text_runs=%d drawing_font_slots=%d\n",
				report.PersianFont,
				report.LatinFont,
				report.MappedCells,
				report.DrawingTextRuns,
				report.DrawingFontSlots,
			)
			return err
		},
	}
}
