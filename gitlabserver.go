package gitlabserver

import (
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"

	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// TODO: implement request timeout

const (
	ITEMS_PER_PAGE = 100
)

type GitlabServer struct {
	client *gitlab.Client
}

func NewGitlabServer(c *gitlab.Client) GitlabServer {
	return GitlabServer{
		client: c,
	}
}

// ProjectCount connects to the git server instance, authenticates
// with the token and obtains the total number of projects
func (g GitlabServer) ProjectCount() (int, error) {
	req, err := g.client.NewRequest("GET", "projects", nil, nil)
	if err != nil {
		return 0, err
	}

	res, err := g.client.Do(req, nil)
	if err != nil {
		return 0, err
	}

	defer res.Body.Close()

	count, err := strconv.Atoi(res.Header["X-Total"][0])
	if err != nil {
		return 0, err
	}

	return count, nil
}

// GroupCount connects to an gitlab instance, authenticates
// with the token and obtains the total number of groups
func (g GitlabServer) GroupCount() (int, error) {
	req, err := g.client.NewRequest("GET", "groups", nil, nil)
	if err != nil {
		return 0, err
	}

	res, err := g.client.Do(req, nil)
	if err != nil {
		return 0, err
	}

	defer res.Body.Close()

	count, err := strconv.Atoi(res.Header["X-Total"][0])
	if err != nil {
		return 0, err
	}

	return count, nil
}

// UserCount connects to the gitlab instance, authenticates
// with the token and obtains the total number of users
func (g GitlabServer) UserCount() (int, error) {
	req, err := g.client.NewRequest("GET", "users", nil, nil)
	if err != nil {
		return 0, err
	}

	res, err := g.client.Do(req, nil)
	if err != nil {
		return 0, err
	}

	defer res.Body.Close()

	count, err := strconv.Atoi(res.Header["X-Total"][0])
	if err != nil {
		return 0, err
	}

	return count, nil
}

// Projects returns a slice with all the projects in gitlab
func (g GitlabServer) Projects() ([]*gitlab.Project, error) {
	projectCount, err := g.ProjectCount()
	if err != nil {
		return nil, err
	}

	// slice that holds all the projects (declared with initial cap to avoid reallocs)
	projects := make([]*gitlab.Project, 0, projectCount)

	pagesToCheck := int(math.Ceil(float64(projectCount) / ITEMS_PER_PAGE))

	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(pagesToCheck)

	// spin one goroutine for each page, which will get PROJECTS_PER_PAGE projects
	// & add those projects to a slice (protected by a mutex)
	for page := 1; page < pagesToCheck+1; page++ { // pages start in 1
		fmt.Printf("[DEBUG] Scanning projects page %d of %d\n", page, pagesToCheck)

		go func(wg *sync.WaitGroup, page int) {
			defer wg.Done()

			opt := &gitlab.ListProjectsOptions{
				ListOptions: gitlab.ListOptions{
					PerPage: ITEMS_PER_PAGE,
					Page:    page,
				},
				Archived: &[]bool{false}[0], // avoid archived repos
			}

			p, resp, err := g.client.Projects.ListProjects(opt)
			if err != nil {
				fmt.Printf("error %q listing projects page %d: %s", err, page, resp.Status)
			}

			mu.Lock()
			projects = append(projects, p...)
			mu.Unlock()
		}(&wg, page)

	}

	wg.Wait()

	return projects, nil
}

// Groups returns a slice with all the groups in gitlab
func (g GitlabServer) Groups() ([]*gitlab.Group, error) {
	groupCount, err := g.GroupCount()
	if err != nil {
		return nil, err
	}

	// slice that holds all the groups (declared with initial cap to avoid reallocs)
	groups := make([]*gitlab.Group, 0, groupCount)

	// gather all the gitlab.Group objects into groups var
	listGroupsOptions := &gitlab.ListGroupsOptions{
		ListOptions:  gitlab.ListOptions{PerPage: ITEMS_PER_PAGE, Page: 1},
		TopLevelOnly: &[]bool{true}[0], // hasta que tengamos la v13 en adelante no funciona.. habrá que hardcodear hasta entonces
	}

	for {
		g, resp, err := g.client.Groups.ListGroups(listGroupsOptions)
		if err != nil {
			return nil, err
		}

		groups = append(groups, g...)
		if resp.NextPage == 0 {
			break
		}
		listGroupsOptions.Page = resp.NextPage
	}

	return groups, nil
}

// TopLevelGroups returns an slice with all the top level
// groups of "groups", without repetitions
func (g GitlabServer) TopLevelGroups(groups []*gitlab.Group) []string {
	var topLevelGroups []string

	for _, group := range groups {
		fullPath := strings.Split(group.FullPath, g.client.BaseURL().Host)
		topLevelGroup := strings.Split(fullPath[0], "/")
		if !slices.Contains(topLevelGroups, topLevelGroup[0]) {
			topLevelGroups = append(topLevelGroups, topLevelGroup[0])
		}
	}

	return topLevelGroups
}

// ParentGroup returns the parent group of a gitlab project
func (g GitlabServer) ParentGroup(p *gitlab.Project) string {
	fullPath := strings.Split(p.WebURL, g.client.BaseURL().Host)
	parentgroup := strings.Split(fullPath[1], "/")

	return parentgroup[1]
}

// GetLatestCommit returns the hash of the latest commit of the project
func (g GitlabServer) GetLatestCommit(p *gitlab.Project) (string, error) {
	commits, resp, err := g.client.Commits.ListCommits(p.ID, nil, nil)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status code %d not expected, expecting %d", resp.StatusCode, http.StatusOK)
	}

	// empty repo
	if len(commits) == 0 {
		return "", fmt.Errorf("this repo has no commits")
	}

	return commits[0].ID, nil
}

// ProjectExists returns a boolean whether the project exists or not
func (g GitlabServer) ProjectExists(gid int, projectName string) (bool, *gitlab.Response, error) {
	// search project name within all projects of group
	listGroupProjectsOptions := &gitlab.ListGroupProjectsOptions{
		ListOptions:      gitlab.ListOptions{PerPage: ITEMS_PER_PAGE, Page: 1},
		OrderBy:          &[]string{"name"}[0],
		Sort:             &[]string{"asc"}[0],
		IncludeSubGroups: &[]bool{false}[0],
		Search:           &projectName,
	}

	gitlabProjects, resp, err := g.client.Groups.ListGroupProjects(gid, listGroupProjectsOptions, nil)
	if err != nil {
		return false, resp, err
	}

	if len(gitlabProjects) == 0 {
		return false, nil, nil
	}

	// check if there is a project with exact same name as the desired project
	for _, gitlabProject := range gitlabProjects {
		if projectName != gitlabProject.Name {
			return false, nil, nil
		} else {
			continue
		}
	}

	return true, nil, nil
}

// GroupExists returns bool if the group already exists
func (g GitlabServer) GroupExists(gid int) (bool, *gitlab.Response, error) {
	// if group does not exist, the API returns 404
	_, resp, err := g.client.Groups.GetGroup(gid, &gitlab.GetGroupOptions{})
	if err != nil {
		return false, resp, err
	}

	return true, nil, nil
}

// AddMember adds Gitlab's user referenced by "userID" as a member of the project "p" with "accessLevel"
func (g GitlabServer) AddMember(p *gitlab.Project, userID int, accessLevel *gitlab.AccessLevelValue) (*gitlab.Response, error) {
	optsMembers := gitlab.AddProjectMemberOptions{
		UserID:      userID,
		AccessLevel: accessLevel,
	}

	_, resp, err := g.client.ProjectMembers.AddProjectMember(p.ID, &optsMembers)
	if err != nil {
		return resp, err
	}

	return resp, nil
}

// AddWebhook adds a webhook to the project pointing to the URL of "webhook"
func (g GitlabServer) AddWebhook(gitlabAPI *gitlab.Client, webhook string, p *gitlab.Project) (*gitlab.Response, error) {
	optsHook := gitlab.AddProjectHookOptions{
		EnableSSLVerification: &[]bool{true}[0],
		PushEvents:            &[]bool{true}[0],
		URL:                   &[]string{webhook}[0],
	}

	_, resp, err := gitlabAPI.Projects.AddProjectHook(p.ID, &optsHook)
	if err != nil {
		return resp, err
	}

	return resp, nil
}
