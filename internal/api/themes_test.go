package api

import "testing"

func TestGitHubRepositoryFromGitURL(t *testing.T) {
	for _, value := range []string{"https://github.com/owner/repository.git", "https://github.com/owner/repository"} {
		if got, ok := githubRepositoryFromGitURL(value); !ok || got != "owner/repository" {
			t.Fatalf("%q => %q, %v", value, got, ok)
		}
	}
	for _, value := range []string{"git@github.com:owner/repository.git", "https://gitlab.com/owner/repository.git", "https://github.com/owner/repository/tree/main", "https://github.com/owner/repo?x=1"} {
		if _, ok := githubRepositoryFromGitURL(value); ok {
			t.Fatalf("accepted unsafe or unsupported URL %q", value)
		}
	}
}
