package spotguide

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	"github.com/banzaicloud/pipeline/auth"
	"github.com/banzaicloud/pipeline/config"
	"github.com/banzaicloud/pipeline/secret"
	"github.com/ghodss/yaml"
	"github.com/google/go-github/github"
	"github.com/goph/emperror"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
	"golang.org/x/oauth2"
)

const SpotguideGithubTopic = "spotguide"
const SpotguideGithubOrganization = "banzaicloud"
const SpotguideYAMLPath = ".banzaicloud/spotguide.yaml"
const PipelineYAMLPath = ".banzaicloud/pipeline.yaml"

var ctx = context.Background()

type Spotguide struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Tags        []string   `json:"tags"`
	Resources   Resources  `json:"resources"`
	Questions   []Question `json:"questions"`
}

type Resources struct {
	CPU         int      `json:"sumCpu"`
	Memory      int      `json:"sumMem"`
	Filters     []string `json:"filters"`
	SameSize    bool     `json:"sameSize"`
	OnDemandPct int      `json:"onDemandPct"`
	MinNodes    int      `json:"minNodes"`
	MaxNodes    int      `json:"maxNodes"`
}

type Question struct {
}

type Repo struct {
	ID           uint       `gorm:"primary_key" json:"-"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
	DeletedAt    *time.Time `sql:"index" json:"-"`
	Name         string     `json:"name"`
	Icon         string     `json:"-"`
	SpotguideRaw []byte     `json:"-" sql:"size:10240"`
	Spotguide    Spotguide  `gorm:"-" json:"spotguide"`
}

func (Repo) TableName() string {
	return "spotguide_repos"
}

func (s *Repo) AfterFind() error {
	return yaml.Unmarshal(s.SpotguideRaw, &s.Spotguide)
}

type LaunchRequest struct {
	SpotguideName    string                       `json:"spotguideName"`
	RepoOrganization string                       `json:"repoOrganization"`
	RepoName         string                       `json:"repoName"`
	Secrets          []secret.CreateSecretRequest `json:"secrets"`
}

type Secret struct {
	Type   string            `json:"type"`
	Values map[string]string `json:"values"`
}

func (r LaunchRequest) RepoFullname() string {
	return r.RepoOrganization + "/" + r.RepoName
}

func getUserGithubToken(userID uint) (string, error) {
	token, err := auth.TokenStore.Lookup(fmt.Sprint(userID), auth.GithubTokenID)
	if err != nil {
		return "", err
	}
	if token == nil {
		return "", fmt.Errorf("Github token not found for user")
	}
	return token.Value, nil
}

func newGithubClientForUser(userID uint) (*github.Client, error) {
	accessToken, err := getUserGithubToken(userID)
	if err != nil {
		return nil, err
	}

	return newGithubClient(accessToken), nil
}

func newGithubClient(accessToken string) *github.Client {
	httpClient := oauth2.NewClient(
		ctx,
		oauth2.StaticTokenSource(&oauth2.Token{AccessToken: accessToken}),
	)

	return github.NewClient(httpClient)
}

func downloadGithubFile(githubClient *github.Client, owner, repo, file string) ([]byte, error) {
	reader, err := githubClient.Repositories.DownloadContents(ctx, owner, repo, file, nil)
	if err != nil {
		return nil, err
	}

	return ioutil.ReadAll(reader)
}

func ScrapeSpotguides() error {

	db := config.DB()

	githubClient := newGithubClient(viper.GetString("github.token"))

	var allRepositories []*github.Repository
	listOpts := github.ListOptions{PerPage: 100}
	for {
		repositories, resp, err := githubClient.Repositories.ListByOrg(ctx, SpotguideGithubOrganization, &github.RepositoryListByOrgOptions{
			ListOptions: listOpts,
		})

		if err != nil {
			return emperror.Wrap(err, "failed to list github repositories")
		}

		allRepositories = append(allRepositories, repositories...)

		if resp.NextPage == 0 {
			break
		}

		listOpts.Page = resp.NextPage
	}

	for _, repository := range allRepositories {
		for _, topic := range repository.Topics {
			if topic == SpotguideGithubTopic {
				owner := repository.GetOwner().GetLogin()
				name := repository.GetName()

				spotguideRaw, err := downloadGithubFile(githubClient, owner, name, SpotguideYAMLPath)
				if err != nil {
					return emperror.Wrap(err, "failed to download spotguide YAML")
				}

				model := Repo{
					Name:         repository.GetFullName(),
					SpotguideRaw: spotguideRaw,
				}

				err = db.Where(&model).Assign(&model).FirstOrCreate(&Repo{}).Error

				if err != nil {
					return err
				}

				break
			}
		}
	}

	return nil
}

func GetSpotguides() ([]*Repo, error) {
	db := config.DB()
	spotguides := []*Repo{}
	err := db.Find(&spotguides).Error
	return spotguides, err
}

func GetSpotguide(name string) (*Repo, error) {
	db := config.DB()
	spotguide := Repo{}
	err := db.Where("name = ?", name).Find(&spotguide).Error
	return &spotguide, err
}

// curl -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -v http://localhost:9090/api/v1/orgs/1/spotguides -d '{"repoName":"spotguide-test", "repoOrganization":"banzaicloud-test", "spotguideName":"banzaicloud/spotguide-nodejs-mongodb"}'
func LaunchSpotguide(request *LaunchRequest, httpRequest *http.Request, orgID, userID uint) error {

	sourceRepo, err := GetSpotguide(request.SpotguideName)
	if err != nil {
		return errors.Wrap(err, "Failed to find spotguide repo")
	}

	err = createSecrets(request, orgID, userID)
	if err != nil {
		return errors.Wrap(err, "Failed to create secrets for spotguide")
	}

	err = createGithubRepo(request, userID, sourceRepo)
	if err != nil {
		return errors.Wrap(err, "Failed to create GitHub repository")
	}

	err = enableCICD(request, httpRequest)
	if err != nil {
		return errors.Wrap(err, "Failed to enable CI/CD for spotguide")
	}

	return nil
}

func preparePipelineYAML(request *LaunchRequest, sourceRepo *Repo, pipelineYAML []byte) ([]byte, error) {
	// Create repo config that drives the CICD flow from LaunchRequest
	repoConfig, err := createDroneRepoConfig(pipelineYAML, request)
	if err != nil {
		return nil, errors.Wrap(err, "failed to initialize repo config")
	}

	repoConfigRaw, err := yaml.Marshal(repoConfig)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal repo config")
	}

	return repoConfigRaw, nil
}

func getSpotguideContent(githubClient *github.Client, request *LaunchRequest, sourceRepo *Repo) ([]github.TreeEntry, error) {
	// Download source repo zip
	sourceRepoParts := strings.Split(sourceRepo.Name, "/")
	sourceRepoOwner := sourceRepoParts[0]
	sourceRepoName := sourceRepoParts[1]

	sourceRelease, _, err := githubClient.Repositories.GetReleaseByTag(ctx, sourceRepoOwner, sourceRepoName, "spotguide")
	if err != nil {
		return nil, errors.Wrap(err, "failed to find source spotguide repository release")
	}

	resp, err := http.Get(sourceRelease.GetZipballURL())
	if err != nil {
		return nil, errors.Wrap(err, "failed to download source spotguide repository release")
	}

	defer resp.Body.Close()
	repoBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "failed to download source spotguide repository release")
	}

	zipReader, err := zip.NewReader(bytes.NewReader(repoBytes), int64(len(repoBytes)))
	if err != nil {
		return nil, errors.Wrap(err, "failed to extract source spotguide repository release")
	}

	// List the files here that needs to be created in this commit and create a tree from them
	entries := []github.TreeEntry{}

	for _, zf := range zipReader.File {
		if zf.FileInfo().IsDir() {
			continue
		}

		file, err := zf.Open()
		if err != nil {
			return nil, errors.Wrap(err, "failed to extract source spotguide repository release")
		}

		content, err := ioutil.ReadAll(file)
		if err != nil {
			return nil, errors.Wrap(err, "failed to extract source spotguide repository release")
		}

		path := strings.SplitN(zf.Name, "/", 2)[1]

		// TODO We don't want to prepare yet, use the same pipeline.yml
		if path == PipelineYAMLPath+"disabled" {
			content, err = preparePipelineYAML(request, sourceRepo, content)
			if err != nil {
				return nil, errors.Wrap(err, "failed to prepare pipeline.yaml")
			}
		}

		entry := github.TreeEntry{
			Type:    github.String("blob"),
			Path:    github.String(path),
			Content: github.String(string(content)),
			Mode:    github.String("100644"),
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

func createGithubRepo(request *LaunchRequest, userID uint, sourceRepo *Repo) error {
	githubClient, err := newGithubClientForUser(userID)
	if err != nil {
		return errors.Wrap(err, "failed to create GitHub client")
	}

	repo := github.Repository{
		Name:        github.String(request.RepoName),
		Description: github.String("Spotguide by BanzaiCloud"),
	}

	// If the user's name is used as organization name, it has to be cleared in repo create.
	// See: https://developer.github.com/v3/repos/#create
	orgName := request.RepoOrganization
	if auth.GetUserNickNameById(userID) == orgName {
		orgName = ""
	}

	_, _, err = githubClient.Repositories.Create(ctx, orgName, &repo)
	if err != nil {
		return errors.Wrap(err, "failed to create spotguide repository")
	}

	log.Infof("Created spotguide repository: %s/%s", request.RepoOrganization, request.RepoName)

	// An initial files have to be created with the API to be able to use the fresh repo
	createFile := &github.RepositoryContentFileOptions{
		Content: []byte("# Say hello to Spotguides!"),
		Message: github.String("initial import"),
	}

	contentResponse, _, err := githubClient.Repositories.CreateFile(ctx, request.RepoOrganization, request.RepoName, "README.md", createFile)

	if err != nil {
		return errors.Wrap(err, "failed to initialize spotguide repository")
	}

	// Prepare the spotguide commit
	spotguideEntries, err := getSpotguideContent(githubClient, request, sourceRepo)
	if err != nil {
		return errors.Wrap(err, "failed to prepare spotguide git content")
	}

	tree, _, err := githubClient.Git.CreateTree(ctx, request.RepoOrganization, request.RepoName, contentResponse.GetSHA(), spotguideEntries)

	if err != nil {
		return errors.Wrap(err, "failed to create git tree for spotguide repository")
	}

	// Create a commit from the tree
	contentResponse.Commit.SHA = contentResponse.SHA

	commit := &github.Commit{
		Message: github.String("adding spotguide structure"),
		Parents: []github.Commit{contentResponse.Commit},
		Tree:    tree,
	}

	newCommit, _, err := githubClient.Git.CreateCommit(ctx, request.RepoOrganization, request.RepoName, commit)

	if err != nil {
		return errors.Wrap(err, "failed to create git commit for spotguide repository")
	}

	// Attach the commit to the master branch.
	// This can be changed later to another branch + create PR.
	// See: https://github.com/google/go-github/blob/master/example/commitpr/main.go#L62
	ref, _, err := githubClient.Git.GetRef(ctx, request.RepoOrganization, request.RepoName, "refs/heads/master")
	if err != nil {
		return errors.Wrap(err, "failed to get git ref for spotguide repository")
	}

	ref.Object.SHA = newCommit.SHA

	_, _, err = githubClient.Git.UpdateRef(ctx, request.RepoOrganization, request.RepoName, ref, false)

	if err != nil {
		return errors.Wrap(err, "failed to update git ref for spotguide repository")
	}

	return nil
}

func createSecrets(request *LaunchRequest, orgID, userID uint) error {

	repoTag := "repo:" + request.RepoFullname()

	for _, secretRequest := range request.Secrets {

		secretRequest.Tags = append(secretRequest.Tags, repoTag)

		if _, err := secret.Store.Store(orgID, &secretRequest); err != nil {
			return errors.Wrap(err, "failed to create spotguide secret:"+secretRequest.Name)
		}
	}

	log.Infof("Created secrets for spotguide: %s/%s", request.RepoOrganization, request.RepoName)

	return nil
}

func enableCICD(request *LaunchRequest, httpRequest *http.Request) error {

	droneClient, err := auth.NewDroneClient(httpRequest)
	if err != nil {
		return errors.Wrap(err, "failed to create Drone client")
	}

	_, err = droneClient.RepoListOpts(true, true)
	if err != nil {
		return errors.Wrap(err, "failed to sync Drone repositories")
	}

	_, err = droneClient.RepoPost(request.RepoOrganization, request.RepoName)
	if err != nil {
		return errors.Wrap(err, "failed to sync enable Drone repository")
	}

	return nil
}

func createDroneRepoConfig(initConfig []byte, request *LaunchRequest) (*droneRepoConfig, error) {
	repoConfig := new(droneRepoConfig)
	if err := yaml.Unmarshal(initConfig, repoConfig); err != nil {
		return nil, err
	}

	// Configure secrets
	if err := droneRepoConfigSecrets(request, repoConfig); err != nil {
		return nil, err
	}

	return repoConfig, nil
}

func droneRepoConfigSecrets(request *LaunchRequest, repoConfig *droneRepoConfig) error {

	if len(request.Secrets) == 0 {
		return nil
	}

	for _, plugin := range repoConfig.Pipeline {
		for _, secret := range request.Secrets {
			plugin.Secrets = append(plugin.Secrets, secret.Name)
		}
	}

	return nil
}
