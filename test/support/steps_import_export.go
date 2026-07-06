package support

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/cucumber/godog"
	"gopkg.in/yaml.v3"
)

// importExportState drives import_export.feature's REST-side assertions.
// MCP-side calls reuse steps_mcp.go's existing generic step patterns
// directly (export_config/import_config are ordinary tools, nothing here
// needs special MCP-side handling).
type importExportState struct {
	s *appState

	lastExportStatus int
	lastExportBody   []byte

	lastImportStatus int
}

// exportedBundleForTest is a minimal YAML-tagged mirror of dto.SeedBundleDTO
// sufficient for these assertions (name/host presence checks) without this
// test file needing to import the production dto package.
type exportedBundleForTest struct {
	Space     string `yaml:"space"`
	Upstreams []struct {
		MatchHost string `yaml:"match_host"`
	} `yaml:"upstreams"`
	Mocks []struct {
		Name string `yaml:"name"`
	} `yaml:"mocks"`
}

func (i *importExportState) iExportTheSpaceOverREST(ctx context.Context, space string) error {
	url := fmt.Sprintf("http://%s/__lyrebird/export", i.s.app.ControlAddr())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build export request: %w", err)
	}
	req.Header.Set("X-Lyrebird-Space", space)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET /__lyrebird/export: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read export response body: %w", err)
	}
	i.lastExportStatus, i.lastExportBody = resp.StatusCode, body
	return nil
}

func (i *importExportState) theExportResponseStatusIs(want int) error {
	if i.lastExportStatus != want {
		return fmt.Errorf("export status = %d, want %d (body: %s)", i.lastExportStatus, want, i.lastExportBody)
	}
	return nil
}

func (i *importExportState) decodeLastExport() (exportedBundleForTest, error) {
	var bundle exportedBundleForTest
	if err := yaml.Unmarshal(i.lastExportBody, &bundle); err != nil {
		return exportedBundleForTest{}, fmt.Errorf("decode exported bundle: %w (body: %s)", err, i.lastExportBody)
	}
	return bundle, nil
}

func (i *importExportState) theExportedBundleIncludesAMockNamed(name string) error {
	bundle, err := i.decodeLastExport()
	if err != nil {
		return err
	}
	for _, m := range bundle.Mocks {
		if m.Name == name {
			return nil
		}
	}
	return fmt.Errorf("exported bundle %+v does not include a mock named %q", bundle, name)
}

func (i *importExportState) theExportedBundleDoesNotIncludeAMockNamed(name string) error {
	bundle, err := i.decodeLastExport()
	if err != nil {
		return err
	}
	for _, m := range bundle.Mocks {
		if m.Name == name {
			return fmt.Errorf("exported bundle unexpectedly includes a mock named %q", name)
		}
	}
	return nil
}

func (i *importExportState) theExportedBundleIncludesAnUpstreamForHost(host string) error {
	bundle, err := i.decodeLastExport()
	if err != nil {
		return err
	}
	for _, u := range bundle.Upstreams {
		if u.MatchHost == host {
			return nil
		}
	}
	return fmt.Errorf("exported bundle %+v does not include an upstream for host %q", bundle, host)
}

func (i *importExportState) doImport(ctx context.Context, space string, body []byte) error {
	url := fmt.Sprintf("http://%s/__lyrebird/import", i.s.app.ControlAddr())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build import request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-yaml")
	req.Header.Set("X-Lyrebird-Space", space)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST /__lyrebird/import: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	i.lastImportStatus = resp.StatusCode
	return nil
}

func (i *importExportState) iImportTheLastExportedBundleIntoSpace(ctx context.Context, space string) error {
	return i.doImport(ctx, space, i.lastExportBody)
}

func (i *importExportState) iImportTheFollowingMalformedBodyIntoSpace(ctx context.Context, space string, body *godog.DocString) error {
	return i.doImport(ctx, space, []byte(body.Content))
}

func (i *importExportState) theImportResponseStatusIs(want int) error {
	if i.lastImportStatus != want {
		return fmt.Errorf("import status = %d, want %d", i.lastImportStatus, want)
	}
	return nil
}

// RegisterImportExportSteps wires import_export.feature's REST-side steps
// against the shared appState s.
func RegisterImportExportSteps(sc *godog.ScenarioContext, s *appState) {
	i := &importExportState{s: s}

	sc.Step(`^I export the space "([^"]*)" over REST$`, i.iExportTheSpaceOverREST)
	sc.Step(`^the export response status is (\d+)$`, i.theExportResponseStatusIs)
	sc.Step(`^the exported bundle includes a mock named "([^"]*)"$`, i.theExportedBundleIncludesAMockNamed)
	sc.Step(`^the exported bundle does not include a mock named "([^"]*)"$`, i.theExportedBundleDoesNotIncludeAMockNamed)
	sc.Step(`^the exported bundle includes an upstream for host "([^"]*)"$`, i.theExportedBundleIncludesAnUpstreamForHost)

	sc.Step(`^I import the last exported bundle into space "([^"]*)"$`, i.iImportTheLastExportedBundleIntoSpace)
	sc.Step(`^I import the following malformed body into space "([^"]*)":$`, i.iImportTheFollowingMalformedBodyIntoSpace)
	sc.Step(`^the import response status is (\d+)$`, i.theImportResponseStatusIs)
}
