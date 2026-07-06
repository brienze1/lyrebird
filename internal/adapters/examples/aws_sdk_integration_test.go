// aws_sdk_integration_test.go is T060: a real aws-sdk-go-v2 client calling
// Lyrebird through the aws-sns recipe (external package avoids an import cycle with bootstrap).
package examples_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sns"

	"github.com/brienze1/lyrebird/internal/adapters/examples"
	"github.com/brienze1/lyrebird/internal/bootstrap"
	"github.com/brienze1/lyrebird/internal/infra/config"
)

func TestAWSSDKClientPublishesThroughTheSNSRecipe(t *testing.T) {
	dataDir := t.TempDir()
	seedDir := filepath.Join(dataDir, "config")
	if err := os.MkdirAll(seedDir, 0o755); err != nil {
		t.Fatalf("mkdir seed dir: %v", err)
	}

	ctx := context.Background()
	cfg := config.Config{
		DataPlaneAddr:    "127.0.0.1:0",
		ControlPlaneAddr: "127.0.0.1:0",
		DefaultSpace:     "default",
		TrafficTTL:       time.Hour,
		TokenTTL:         time.Hour,
		BodyCapBytes:     1 << 20,
		UpstreamTimeout:  10 * time.Second,
		DBPath:           filepath.Join(dataDir, "lyrebird.db"),
		SeedDir:          seedDir,
		GCInterval:       time.Hour,
	}
	app, err := bootstrap.Run(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("bootstrap.Run: %v", err)
	}
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })

	recipe, ok := examples.Get("aws-sns")
	if !ok {
		t.Fatal(`examples.Get("aws-sns") not found`)
	}
	if err := postMock(ctx, app.ControlAddr(), recipe.Mock); err != nil {
		t.Fatalf("install aws-sns recipe's mock: %v", err)
	}

	client := sns.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("dummy-access-key", "dummy-secret-key", ""),
	}, func(o *sns.Options) {
		o.BaseEndpoint = aws.String("http://" + app.DataAddr())
	})

	out, err := client.Publish(ctx, &sns.PublishInput{
		TopicArn: aws.String("arn:aws:sns:us-east-1:000000000000:example-topic"),
		Message:  aws.String("hello from a real AWS SDK client"),
	})
	if err != nil {
		t.Fatalf("Publish (via a real aws-sdk-go-v2 client pointed at Lyrebird): %v", err)
	}
	if out.MessageId == nil || *out.MessageId != "567910cd-659e-55d4-8ccb-5aaf14679dc0" {
		t.Errorf("MessageId = %v, want the aws-sns recipe's fixed MessageId", out.MessageId)
	}
}

func postMock(ctx context.Context, controlAddr string, mock json.RawMessage) error {
	url := fmt.Sprintf("http://%s/__lyrebird/mocks", controlAddr)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(mock))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("POST /__lyrebird/mocks status = %d: %s", resp.StatusCode, body)
	}
	return nil
}
