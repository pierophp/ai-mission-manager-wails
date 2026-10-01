package domain

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ClassifyExternalURL derives an External Object's provider, kind, stable
// identity, and canonical URL from its owning system URL.
func ClassifyExternalURL(raw string) (ExternalObject, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ExternalObject{}, errors.New("the external URL cannot be blank")
	}
	u, err := url.Parse(raw)
	if err == nil && strings.HasPrefix(raw, "https://") && u.Host != "" && u.User == nil {
		host := strings.ToLower(u.Host)
		parts := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
		if (host == "github.com" || host == "www.github.com") && len(parts) == 4 && parts[0] != "" && parts[1] != "" {
			kind := ExternalObjectKind("")
			switch parts[2] {
			case "issues":
				kind = ObjectIssue
			case "pull":
				kind = ObjectPullRequest
			}
			if kind != "" {
				if n, e := strconv.ParseUint(parts[3], 10, 64); e == nil {
					owner, repo := strings.ToLower(parts[0]), strings.ToLower(parts[1])
					segment, keyKind := "issues", "issue"
					if kind == ObjectPullRequest {
						segment, keyKind = "pull", "pull"
					}
					canonical := fmt.Sprintf("https://github.com/%s/%s/%s/%d", owner, repo, segment, n)
					return ExternalObject{Provider: ProviderGitHub, Kind: kind, ExternalKey: fmt.Sprintf("%s:%s/%s#%d", keyKind, owner, repo, n), CanonicalURL: canonical}, nil
				}
			}
		}
		if strings.HasSuffix(host, ".atlassian.net") {
			site := strings.TrimSuffix(host, ".atlassian.net")
			if site != "" && len(parts) >= 2 && parts[0] == "browse" && validExternalJiraKey(parts[1]) {
				key := parts[1]
				return ExternalObject{Provider: ProviderAtlassian, Kind: ObjectIssue, ExternalKey: "jira:" + site + "#" + key, CanonicalURL: "https://" + site + ".atlassian.net/browse/" + key}, nil
			}
			if site != "" && len(parts) >= 5 && parts[0] == "wiki" && parts[1] == "spaces" && parts[2] != "" && parts[3] == "pages" {
				if n, e := strconv.ParseUint(parts[4], 10, 64); e == nil {
					return ExternalObject{Provider: ProviderAtlassian, Kind: ObjectDocument, ExternalKey: fmt.Sprintf("confluence:%s#%d", site, n), CanonicalURL: fmt.Sprintf("https://%s.atlassian.net/wiki/spaces/%s/pages/%d", site, parts[2], n)}, nil
				}
			}
		}
		if host == "bitbucket.org" && len(parts) == 4 && parts[0] != "" && parts[1] != "" && parts[2] == "pull-requests" {
			if n, e := strconv.ParseUint(parts[3], 10, 64); e == nil {
				ws, repo := strings.ToLower(parts[0]), strings.ToLower(parts[1])
				return ExternalObject{Provider: ProviderAtlassian, Kind: ObjectPullRequest, ExternalKey: fmt.Sprintf("bitbucket:%s/%s#%d", ws, repo, n), CanonicalURL: fmt.Sprintf("https://bitbucket.org/%s/%s/pull-requests/%d", ws, repo, n)}, nil
			}
		}
		if host == "dev.azure.com" && len(parts) >= 4 {
			org, project := strings.ToLower(parts[0]), strings.ToLower(parts[1])
			if org != "" && project != "" && len(parts) >= 5 && parts[2] == "_workitems" && parts[3] == "edit" {
				if n, e := strconv.ParseUint(parts[4], 10, 64); e == nil {
					return ExternalObject{Provider: ProviderAzureDevOps, Kind: ObjectIssue, ExternalKey: fmt.Sprintf("ado:%s/%s#%d", org, project, n), CanonicalURL: fmt.Sprintf("https://dev.azure.com/%s/%s/_workitems/edit/%d", org, project, n)}, nil
				}
			}
			if org != "" && project != "" && len(parts) >= 6 && parts[2] == "_git" && parts[3] != "" && parts[4] == "pullrequest" {
				if n, e := strconv.ParseUint(parts[5], 10, 64); e == nil {
					repo := strings.ToLower(parts[3])
					return ExternalObject{Provider: ProviderAzureDevOps, Kind: ObjectPullRequest, ExternalKey: fmt.Sprintf("ado:%s/%s#%d", org, project, n), CanonicalURL: fmt.Sprintf("https://dev.azure.com/%s/%s/_git/%s/pullrequest/%d", org, project, repo, n)}, nil
				}
			}
		}
	}
	return ExternalObject{Provider: ProviderGeneric, Kind: ObjectGeneric, ExternalKey: raw, CanonicalURL: raw}, nil
}

func validExternalJiraKey(value string) bool {
	project, number, ok := strings.Cut(value, "-")
	if !ok || project == "" || number == "" {
		return false
	}
	for _, c := range project {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	for _, c := range number {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
