package paradox

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// CompanyInfo represents the company information from company.inf
type CompanyInfo struct {
	Name      string `json:"name"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
}

// ReadCompanyInfo reads and parses a company.inf file
func ReadCompanyInfo(path string, converter func(string) string) (*CompanyInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open company.inf: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lines := []string{}

	for scanner.Scan() {
		line := scanner.Text()
		lines = append(lines, line)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read company.inf: %w", err)
	}

	if len(lines) < 3 {
		return nil, fmt.Errorf("company.inf has insufficient lines")
	}

	info := &CompanyInfo{}

	// First line is the company name (encoded)
	if converter != nil {
		info.Name = converter(lines[0])
	} else {
		info.Name = lines[0]
	}

	// Second line is the start date
	info.StartDate = strings.TrimSpace(lines[1])

	// Third line is the end date
	info.EndDate = strings.TrimSpace(lines[2])

	return info, nil
}
