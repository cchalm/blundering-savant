package workspace

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func testSanitizeForBranchName(t *testing.T, input string, expected string) {
	result := sanitizeForBranchName(input)
	require.Equal(t, expected, result)
}

func TestSanitizeForBranchName_Basic(t *testing.T) {
	testSanitizeForBranchName(t, "Simple Title", "simple-title")
}

func TestSanitizeForBranchName_WithNumbers(t *testing.T) {
	testSanitizeForBranchName(t, "Issue 123 Fix", "issue-123-fix")
}

func TestSanitizeForBranchName_WithSpecialCharacters(t *testing.T) {
	testSanitizeForBranchName(t, "Fix: bug @home [urgent]", "fix-bug-home-urgent")
}

func TestSanitizeForBranchName_WithUnderscores(t *testing.T) {
	testSanitizeForBranchName(t, "test_function_name", "test-function-name")
}

func TestSanitizeForBranchName_AlreadyLowercase(t *testing.T) {
	testSanitizeForBranchName(t, "already-lowercase", "already-lowercase")
}

func TestSanitizeForBranchName_MixedCase(t *testing.T) {
	testSanitizeForBranchName(t, "CamelCase-Title", "camelcase-title")
}

func TestSanitizeForBranchName_MultipleSpaces(t *testing.T) {
	testSanitizeForBranchName(t, "multiple   spaces  here", "multiple---spaces--here")
}

func TestSanitizeForBranchName_GitInvalidCharacters(t *testing.T) {
	testSanitizeForBranchName(t, "branch~name^with:bad?chars*", "branchnamewithbadchars")
}

func TestSanitizeForBranchName_UnicodeCharacters(t *testing.T) {
	testSanitizeForBranchName(t, "unicode-café-naïve", "unicode-caf-nave")
}

func TestSanitizeForBranchName_EmptyString(t *testing.T) {
	testSanitizeForBranchName(t, "", "")
}

func TestSanitizeForBranchName_OnlyInvalidCharacters(t *testing.T) {
	testSanitizeForBranchName(t, "~^:?*[]", "")
}