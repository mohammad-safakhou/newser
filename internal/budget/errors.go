package budget

import "fmt"

// ErrExceeded is returned when usage surpasses configured limits.
type ErrExceeded struct {
	Kind  string
	Usage string
	Limit string
}

func (e ErrExceeded) Error() string {
	if e.Limit != "" {
		return fmt.Sprintf("budget %s exceeded: usage=%s limit=%s", e.Kind, e.Usage, e.Limit)
	}
	return fmt.Sprintf("budget %s exceeded: usage=%s", e.Kind, e.Usage)
}

// ErrApprovalRequired indicates that a run needs manual approval before execution.
type ErrApprovalRequired struct {
	EstimatedCost float64
	Threshold     float64
}

func (e ErrApprovalRequired) Error() string {
	return fmt.Sprintf("estimated cost $%.2f exceeds approval threshold $%.2f", e.EstimatedCost, e.Threshold)
}
