package support

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"

	kms "cloud.google.com/go/kms/apiv1"
	"cloud.google.com/go/kms/apiv1/kmspb"
	"github.com/cucumber/godog"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// kmsState drives the real GCP KMS client against Lyrebird's gRPC data plane,
// exactly as the consuming service (wallet-api) does: endpoint override, no
// auth, insecure transport. This proves SC-002 with the genuine client
// library, not a hand-built message.
type kmsState struct {
	s          *appState
	ciphertext []byte
	plaintext  []byte
	err        error
}

func (k *kmsState) decryptStub(stub string) error {
	ct, err := base64.StdEncoding.DecodeString(stub)
	if err != nil {
		return fmt.Errorf("stub is not valid base64: %w", err)
	}
	k.ciphertext = ct

	ctx := context.Background()
	client, err := kms.NewKeyManagementClient(ctx,
		option.WithEndpoint(k.s.app.GRPCAddr()),
		option.WithoutAuthentication(),
		option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
	)
	if err != nil {
		return fmt.Errorf("build KMS client: %w", err)
	}
	defer func() { _ = client.Close() }()

	resp, err := client.Decrypt(ctx, &kmspb.DecryptRequest{
		Name:       "projects/p/locations/global/keyRings/r/cryptoKeys/k",
		Ciphertext: ct,
	})
	if err != nil {
		k.err = err
		return nil // asserted by a later step
	}
	k.plaintext = resp.GetPlaintext()
	return nil
}

func (k *kmsState) plaintextEqualsCiphertext() error {
	if k.err != nil {
		return fmt.Errorf("expected Decrypt to succeed, got: %w", k.err)
	}
	if !bytes.Equal(k.plaintext, k.ciphertext) {
		return fmt.Errorf("plaintext %q != base64-decoded ciphertext %q", k.plaintext, k.ciphertext)
	}
	return nil
}

// RegisterGRPCKMSSteps wires the real-KMS-client acceptance steps.
func RegisterGRPCKMSSteps(sc *godog.ScenarioContext, s *appState) {
	k := &kmsState{s: s}
	sc.Step(`^a KMS client decrypts the base64 ciphertext "([^"]*)"$`, k.decryptStub)
	sc.Step(`^the decrypted plaintext equals the base64-decoded ciphertext$`, k.plaintextEqualsCiphertext)
}
