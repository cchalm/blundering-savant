// Package workspace provides workspace management and validation types.
package workspace

// ValidationResult represents the outcome of validation
type ValidationResult struct {
	Succeeded bool
	Details   string
}