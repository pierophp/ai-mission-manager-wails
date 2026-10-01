package domain

import "testing"

func TestClassifyExternalURL(t *testing.T) {
	tests := []struct {
		url            string
		provider       ExternalProvider
		kind           ExternalObjectKind
		key, canonical string
	}{
		{"https://www.GitHub.com/Acme/App/issues/7?x=1#comment", ProviderGitHub, ObjectIssue, "issue:acme/app#7", "https://github.com/acme/app/issues/7"},
		{"https://github.com/Acme/App/pull/9/", ProviderGitHub, ObjectPullRequest, "pull:acme/app#9", "https://github.com/acme/app/pull/9"},
		{"https://ACME.atlassian.net/browse/proj-3/?x=1", ProviderAtlassian, ObjectIssue, "jira:acme#proj-3", "https://acme.atlassian.net/browse/proj-3"},
		{"https://acme.atlassian.net/wiki/spaces/ENG/pages/456/Runbook", ProviderAtlassian, ObjectDocument, "confluence:acme#456", "https://acme.atlassian.net/wiki/spaces/ENG/pages/456"},
		{"https://bitbucket.org/Team/Repo/pull-requests/89/", ProviderAtlassian, ObjectPullRequest, "bitbucket:team/repo#89", "https://bitbucket.org/team/repo/pull-requests/89"},
		{"https://dev.azure.com/ORG/Proj/_workitems/edit/123?x=1", ProviderAzureDevOps, ObjectIssue, "ado:org/proj#123", "https://dev.azure.com/org/proj/_workitems/edit/123"},
		{"https://dev.azure.com/ORG/Proj/_git/Repo/pullrequest/456/#discussion", ProviderAzureDevOps, ObjectPullRequest, "ado:org/proj#456", "https://dev.azure.com/org/proj/_git/repo/pullrequest/456"},
		{"https://example.com/a?x=1", ProviderGeneric, ObjectGeneric, "https://example.com/a?x=1", "https://example.com/a?x=1"},
		{"https://github.com:8443/acme/app/issues/7", ProviderGeneric, ObjectGeneric, "https://github.com:8443/acme/app/issues/7", "https://github.com:8443/acme/app/issues/7"},
		{"HTTPS://github.com/acme/app/issues/7", ProviderGeneric, ObjectGeneric, "HTTPS://github.com/acme/app/issues/7", "HTTPS://github.com/acme/app/issues/7"},
		{"https://user@github.com/acme/app/issues/7", ProviderGeneric, ObjectGeneric, "https://user@github.com/acme/app/issues/7", "https://user@github.com/acme/app/issues/7"},
		{"https://acme.atlassian.net/browse/project-name-123", ProviderGeneric, ObjectGeneric, "https://acme.atlassian.net/browse/project-name-123", "https://acme.atlassian.net/browse/project-name-123"},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got, err := ClassifyExternalURL(tt.url)
			if err != nil {
				t.Fatal(err)
			}
			if got.Provider != tt.provider || got.Kind != tt.kind || got.ExternalKey != tt.key || got.CanonicalURL != tt.canonical {
				t.Fatalf("ClassifyExternalURL() = %#v", got)
			}
		})
	}
	jira, err := ClassifyExternalURL("https://acme.atlassian.net/browse/PROJ-12345678901234567890")
	if err != nil || jira.Provider != ProviderAtlassian {
		t.Fatalf("long Jira key should classify: %#v, %v", jira, err)
	}
	if _, err := ClassifyExternalURL(" \t"); err == nil {
		t.Fatal("blank URL accepted")
	}
}
