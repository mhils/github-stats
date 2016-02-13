package main

import (
	"fmt"
	"github.com/google/go-github/github"
	"gopkg.in/alecthomas/kingpin.v2"
	"time"
)

var (
	owner            = kingpin.Arg("owner", "GitHub owner.").Required().String()
	repo             = kingpin.Arg("repo", "GitHub repository").Required().String()
	base             = kingpin.Arg("base", "Base tag/commit").Required().String()
	since *time.Time = nil
)

type Result struct {
	Issues   []github.Issue
	Response github.Response
}

func issueWorker(jobs <-chan int, results chan<- Result) {
	for page := range jobs {
		client := github.NewClient(nil)
		issues, response, err := client.Issues.ListByRepo(
			*owner,
			*repo,
			&github.IssueListByRepoOptions{
				State:       "closed",
				Since:       *since,
				ListOptions: github.ListOptions{Page: page, PerPage: 100},
			})
		if err != nil {
			panic(err)
		}
		fmt.Print(".")
		results <- Result{Issues: issues, Response: *response}
	}
}

func main() {
	kingpin.Parse()

	// Compare revisions
	client := github.NewClient(nil)
	comparison, _, err := client.Repositories.CompareCommits(*owner, *repo, *base, "master")
	if err != nil {
		panic(err)
	}
	fmt.Print(".")
	since = comparison.BaseCommit.Commit.Author.Date

	// Gather statistics
	contributors := make(map[string]bool)
	for _, commit := range comparison.Commits {
		contributors[*commit.Commit.Author.Name] = true
	}
	days := int(time.Since(*since).Hours() / 24)
	commits := *comparison.AheadBy

	// Enumerate issues
	jobs := make(chan int, 100)
	results := make(chan Result, 100)
	for w := 1; w <= 5; w++ {
		go issueWorker(jobs, results)
	}

	jobs <- 1
	result := <-results
	pages := result.Response.LastPage
	issues := result.Issues

	for p := 2; p <= pages; p++ {
		jobs <- p
	}
	close(jobs)

	for a := 2; a <= pages; a++ {
		issues = append(issues, (<-results).Issues...)
	}

	closedIssues := 0
	closedPRs := 0
	for _, issue := range issues {
		if issue.ClosedAt.After(*since) {
			if issue.PullRequestLinks != nil {
				closedPRs++
			} else {
				closedIssues++
			}
		}
	}

	fmt.Println("")
	fmt.Printf("Since the last release, the project has had %d commits by %d contributors, "+
		"resulting in %d closed issues and %d closed PRs, all of this in just over %d days.",
		commits, len(contributors), closedIssues, closedPRs, days)
	fmt.Println("")
}
