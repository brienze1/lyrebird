package usecase

import "context"

// mitmCAProvider is the subset of *mitmca.CA's behavior GetMITMCACert
// depends on, named at the point of use per this codebase's convention.
type mitmCAProvider interface {
	CACertPEM() []byte
}

// GetMITMCACert serves Lyrebird's MITM CA certificate (PEM-encoded) to
// whichever control-plane adapter asks — Admin REST's ca-cert route and the
// get_mitm_ca_cert MCP tool both call this one use case, so neither can drift
// from the other (constitution Principle II).
type GetMITMCACert struct {
	ca mitmCAProvider
}

// NewGetMITMCACert builds a GetMITMCACert use case.
func NewGetMITMCACert(ca mitmCAProvider) *GetMITMCACert {
	return &GetMITMCACert{ca: ca}
}

// Execute returns the CA certificate, PEM-encoded. Contains no key material.
func (uc *GetMITMCACert) Execute(_ context.Context) []byte {
	return uc.ca.CACertPEM()
}
