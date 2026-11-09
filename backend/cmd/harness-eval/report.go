package main

import (
	"fmt"

	"github.com/lehlud/zentrale/domain"
)

func formatCost(usage domain.UsageSummary) string {
	label := fmt.Sprintf("%.6f real", usage.RealCost)
	if usage.FictionalCost > 0 {
		label += fmt.Sprintf("; %.6f equivalent", usage.FictionalCost)
	}
	if usage.Estimated {
		label += " (estimated)"
	}
	if usage.UnknownPrices > 0 {
		label += fmt.Sprintf("; incomplete: %d requests without prices", usage.UnknownPrices)
	}
	return label
}
