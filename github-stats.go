package main

import (
	"context"
	"fmt"
	"github.com/google/go-github/github"
	"golang.org/x/oauth2"
	"gopkg.in/alecthomas/kingpin.v2"
	"sync"
	"time"
)

var (
	owner                     = kingpin.Arg("owner", "GitHub owner.").Required().String()
	repo                      = kingpin.Arg("repo", "GitHub repository").Required().String()
	base                      = kingpin.Arg("base", "Base tag/commit").Required().String()
	head                      = kingpin.Arg("head", "Head tag/commit").Default("main").String()
	auth_token                = kingpin.Flag("token", "OAuth Token").Envar("GITHUB_TOKEN").String()
	since      *time.Time     = nil
	client     *github.Client = nil
)

func issueWorker(pages <-chan int, results chan<- github.Issue) {
	for page := range pages {
		issues, _, err := client.Issues.ListByRepo(
			context.Background(),
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
		for _, issue := range issues {
			results <- *issue
		}
	}
}

func commitWorker(pages <-chan int, results chan<- github.RepositoryCommit) {
	for page := range pages {
		compareUrl := fmt.Sprintf("repos/%v/%v/compare/%v...%v?per_page=100&page=%v", *owner, *repo, *base, *head, page)
		req, err := client.NewRequest("GET", compareUrl, nil)
		if err != nil {
			panic(err)
		}
		comp := new(github.CommitsComparison)
		_, err = client.Do(context.Background(), req, comp)
		if err != nil {
			panic(err)
		}
		fmt.Print(".")
		for _, commit := range comp.Commits {
			results <- commit
		}
	}
}

func main() {
	kingpin.Parse()

	fmt.Println("Getting repository data...")

	if *auth_token != "" {
		tc := oauth2.NewClient(
			context.Background(),
			oauth2.StaticTokenSource(
				&oauth2.Token{AccessToken: *auth_token},
			))
		client = github.NewClient(tc)
	} else {
		client = github.NewClient(nil)
	}

	baseCommit, _, err := client.Repositories.GetCommit(context.Background(), *owner, *repo, *base)
	if err != nil {
		panic(err)
	}
	since = baseCommit.Commit.Author.Date

	until := time.Now()
	if *head != "main" {
		headCommit, _, err := client.Repositories.GetCommit(context.Background(), *owner, *repo, *head)
		if err != nil {
			panic(err)
		}
		until = *headCommit.Commit.Author.Date
	}

	_, issueInfo, err := client.Issues.ListByRepo(
		context.Background(),
		*owner,
		*repo,
		&github.IssueListByRepoOptions{
			State:       "closed",
			Since:       *since,
			ListOptions: github.ListOptions{Page: 1, PerPage: 100},
		})
	if err != nil {
		panic(err)
	}
	if issueInfo.LastPage == 0 {
		// if we have only one page, LastPage is not set.
		issueInfo.LastPage = 1
	}

	compareUrl := fmt.Sprintf("repos/%v/%v/compare/%v...%v?per_page=100", *owner, *repo, *base, *head)
	req, err := client.NewRequest("GET", compareUrl, nil)
	if err != nil {
		panic(err)
	}
	commitInfo, err := client.Do(context.Background(), req, nil)
	if err != nil {
		panic(err)
	}
	if commitInfo.LastPage == 0 {
		// if we have only one page, LastPage is not set.
		commitInfo.LastPage = 1
	}

	fmt.Printf("Fetching %d commit pages and %d issue pages...", commitInfo.LastPage, issueInfo.LastPage)
	// Enumerate commits
	commitPages := make(chan int, 100)
	commits := make(chan github.RepositoryCommit, 100000)
	commitWg := &sync.WaitGroup{}
	commitWg.Add(5)
	for w := 1; w <= 5; w++ {
		go func() {
			defer commitWg.Done()
			commitWorker(commitPages, commits)
		}()
	}
	for p := 1; p <= commitInfo.LastPage; p++ {
		commitPages <- p
	}
	close(commitPages)
	go func() {
		commitWg.Wait()
		close(commits)
	}()

	// Enumerate issues
	issuePages := make(chan int, 100)
	issues := make(chan github.Issue, 100000)
	issueWg := &sync.WaitGroup{}
	issueWg.Add(5)
	for w := 1; w <= 5; w++ {
		go func() {
			defer issueWg.Done()
			issueWorker(issuePages, issues)
		}()
	}
	for p := 1; p <= issueInfo.LastPage; p++ {
		issuePages <- p
	}
	close(issuePages)
	go func() {
		issueWg.Wait()
		close(issues)
	}()

	closedIssues := 0
	closedPRs := 0
	for issue := range issues {
		if issue.ClosedAt.After(*since) && issue.ClosedAt.Before(until) {
			if issue.PullRequestLinks != nil {
				closedPRs++
			} else {
				closedIssues++
			}
		}
	}

	contributors := make(map[string]bool)
	commitCount := 0
	for commit := range commits {
		contributors[*commit.Commit.Author.Name] = true
		commitCount += 1
	}
	days := int(until.Sub(*since).Hours() / 24)

	fmt.Println("")
	fmt.Printf("Since the last release, the project has had %d commits by %d contributors, "+
		"resulting in %d closed issues and %d closed pull requests, all of this in just over %d days.",
		commitCount, len(contributors), closedIssues, closedPRs, days)
	fmt.Println("")
}
