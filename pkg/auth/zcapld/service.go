/*
Copyright SecureKey Technologies Inc. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package zcapld

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	cryptoapi "github.com/hyperledger/aries-framework-go/pkg/crypto"
	"github.com/hyperledger/aries-framework-go/pkg/doc/signature/suite"
	"github.com/hyperledger/aries-framework-go/pkg/doc/signature/suite/ed25519signature2018"
	"github.com/hyperledger/aries-framework-go/pkg/doc/util/signature"
	"github.com/hyperledger/aries-framework-go/pkg/doc/verifiable"
	"github.com/hyperledger/aries-framework-go/pkg/kms"
	ariesstorage "github.com/hyperledger/aries-framework-go/pkg/storage"
	"github.com/hyperledger/aries-framework-go/pkg/vdr/fingerprint"
	"github.com/piprate/json-gold/ld"
	"github.com/trustbloc/edge-core/pkg/log"
	"github.com/trustbloc/edge-core/pkg/zcapld"
)

const (
	storeName   = "zcap_capability"
	edvResource = "urn:edv:vault"
)

var logger = log.New("auth-zcap-service")

// Service to provide zcapld functionality
type Service struct {
	keyManager      kms.KeyManager
	crypto          cryptoapi.Crypto
	store           ariesstorage.Store
	cachedLDContext map[string]*ld.RemoteDocument
}

// New return zcap service
func New(keyManager kms.KeyManager, crypto cryptoapi.Crypto, storeProv ariesstorage.Provider) (*Service, error) {
	store, err := storeProv.OpenStore(storeName)
	if err != nil {
		return nil, fmt.Errorf("failed to open store %s: %w", storeName, err)
	}

	ctx, err := loadJSONLDContext()
	if err != nil {
		return nil, fmt.Errorf("failed create json ld document loader: %w", err)
	}

	return &Service{
		keyManager: keyManager, crypto: crypto, store: store, cachedLDContext: ctx,
	}, nil
}

// Create zcap payload
func (s *Service) Create(resourceID, verificationMethod string) ([]byte, error) {
	rootCapability, err := s.createRootCapability(resourceID)
	if err != nil {
		return nil, err
	}

	signer, err := signature.NewCryptoSigner(s.crypto, s.keyManager, kms.ED25519)
	if err != nil {
		return nil, fmt.Errorf("failed to create crypto signer: %w", err)
	}

	_, didKeyURL := fingerprint.CreateDIDKey(signer.PublicKeyBytes())

	capability, err := zcapld.NewCapability(&zcapld.Signer{
		SignatureSuite:     ed25519signature2018.New(suite.WithSigner(signer)),
		SuiteType:          ed25519signature2018.SignatureType,
		VerificationMethod: didKeyURL,
	}, zcapld.WithParent(rootCapability.ID), zcapld.WithInvoker(verificationMethod),
		zcapld.WithAllowedActions("read", "write"), zcapld.WithInvocationTarget(resourceID, edvResource),
		zcapld.WithCapabilityChain(rootCapability.ID))
	if err != nil {
		return nil, fmt.Errorf("failed to create new capability: %w", err)
	}

	capabilityBytes, err := json.Marshal(capability)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal capability: %w", err)
	}

	if err := s.store.Put(capability.ID, capabilityBytes); err != nil {
		return nil, fmt.Errorf("failed to store capability: %w", err)
	}

	return capabilityBytes, nil
}

// Handler will create auth handler
func (s *Service) Handler(resourceID string, req *http.Request, w http.ResponseWriter,
	next http.HandlerFunc) (http.HandlerFunc, error) {
	rootCapability, err := s.getCapability(resourceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get root capability %s from db: %w", resourceID, err)
	}

	action := "write"
	if req.Method == http.MethodGet {
		action = "read"
	}

	cachingDL := verifiable.CachingJSONLDLoader()

	for u, d := range s.cachedLDContext {
		cachingDL.AddDocument(u, d)
	}

	return zcapld.NewHTTPSigAuthHandler(
		&zcapld.HTTPSigAuthConfig{
			CapabilityResolver: capabilityResolver{svc: s},
			KeyResolver:        &zcapld.DIDKeyResolver{},
			VerifierOptions: []zcapld.VerificationOption{
				zcapld.WithSignatureSuites(
					ed25519signature2018.New(suite.WithVerifier(ed25519signature2018.NewPublicKeyVerifier())),
				), zcapld.WithLDDocumentLoaders(cachingDL),
			},
			Secrets:     &zcapld.AriesDIDKeySecrets{},
			ErrConsumer: logError{w: w}.Log,
			KMS:         s.keyManager,
			Crypto:      s.crypto,
		},
		&zcapld.InvocationExpectations{
			Target:         resourceID,
			RootCapability: rootCapability.ID,
			Action:         action,
		},
		next,
	), nil
}

func (s *Service) createRootCapability(resourceID string) (*zcapld.Capability, error) {
	// create root capability and store in db
	signer, err := signature.NewCryptoSigner(s.crypto, s.keyManager, kms.ED25519)
	if err != nil {
		return nil, fmt.Errorf("failed to create crypto signer: %w", err)
	}

	rootID := uuid.New().URN()

	_, didKeyURL := fingerprint.CreateDIDKey(signer.PublicKeyBytes())

	rootCapability, err := zcapld.NewCapability(&zcapld.Signer{
		SignatureSuite:     ed25519signature2018.New(suite.WithSigner(signer)),
		SuiteType:          ed25519signature2018.SignatureType,
		VerificationMethod: didKeyURL,
	}, zcapld.WithID(rootID), zcapld.WithInvocationTarget(resourceID, edvResource),
		zcapld.WithAllowedActions("read", "write"))
	if err != nil {
		return nil, fmt.Errorf("failed to create new root capability: %w", err)
	}

	rootCapabilityBytes, err := json.Marshal(rootCapability)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal root capability: %w", err)
	}

	if err := s.store.Put(rootCapability.ID, rootCapabilityBytes); err != nil {
		return nil, fmt.Errorf("failed to store root capability: %w", err)
	}

	if err := s.store.Put(resourceID, rootCapabilityBytes); err != nil {
		return nil, fmt.Errorf("failed to store root capability: %w", err)
	}

	return rootCapability, nil
}

func (s *Service) getCapability(id string) (*zcapld.Capability, error) {
	bytes, err := s.store.Get(id)
	if err != nil {
		return nil, err
	}

	return zcapld.ParseCapability(bytes)
}

// capabilityResolver resolve capability from db
type capabilityResolver struct {
	svc *Service
}

// Resolve resolves capabilities.
func (s capabilityResolver) Resolve(uri string) (*zcapld.Capability, error) {
	return s.svc.getCapability(uri)
}

// logError log error
type logError struct {
	w http.ResponseWriter
}

// Resolve resolves capabilities.
func (l logError) Log(err error) {
	l.w.WriteHeader(http.StatusBadRequest)

	_, errWrite := l.w.Write([]byte(err.Error()))
	if errWrite != nil {
		logger.Errorf(errWrite.Error())
	}
}

func loadJSONLDContext() (map[string]*ld.RemoteDocument, error) {
	contexts := []struct {
		vocab   string
		content string
	}{
		{
			vocab:   "https://w3id.org/security/v1",
			content: w3idOrgSecurityV1,
		},
		{
			vocab:   "https://w3id.org/security/v2",
			content: w3idOrgSecurityV2,
		},
	}

	cached := make(map[string]*ld.RemoteDocument)

	for i := range contexts {
		ctx := contexts[i]

		reader, err := ld.DocumentFromReader(strings.NewReader(ctx.content))
		if err != nil {
			return nil, fmt.Errorf("failed to read cached jsonld context: %w", err)
		}

		cached[ctx.vocab] = &ld.RemoteDocument{
			DocumentURL: ctx.vocab,
			Document:    reader,
		}
	}

	return cached, nil
}
