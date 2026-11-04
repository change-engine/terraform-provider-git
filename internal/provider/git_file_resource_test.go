package provider

import "testing"

func TestParseResourceID(t *testing.T) {
	repositoryURL, branch, repoPath, err := parseResourceID(resourceID("https://github.com/change-engine/listen.git", "main", ".github/workflows/staging.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if repositoryURL != "https://github.com/change-engine/listen.git" || branch != "main" || repoPath != ".github/workflows/staging.yml" {
		t.Fatalf("got %q, %q, %q", repositoryURL, branch, repoPath)
	}

	for _, id := range []string{"", "https://github.com/change-engine/listen.git|main", "|main|README.md", "https://github.com/change-engine/listen.git||README.md"} {
		if _, _, _, err := parseResourceID(id); err == nil {
			t.Errorf("parseResourceID(%q) succeeded", id)
		}
	}
}
