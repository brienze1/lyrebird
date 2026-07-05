Feature: Control-plane bearer-token auth
  As an operator sharing a Lyrebird deployment with other agents/users
  I want the control plane to require a token only when I explicitly configure one
  So that a single env var hardens a shared deployment without ever touching the data plane (FR-030/031, SC-007)

  Background:
    Given a fresh temporary Lyrebird data directory

  Scenario: The control plane is open by default (no auth keys configured)
    Given Lyrebird boots
    When I call REST "GET /__lyrebird/mocks" with no bearer token
    Then the control-plane call responds with status 200
    And an MCP client is connected to the control plane
    And I call the MCP tool "list_mocks" with arguments '{}'
    Then the MCP call succeeds

  Scenario: Auth enabled rejects an unauthenticated REST control-plane call
    Given the auth keys are configured to "secret-1"
    And Lyrebird boots
    When I call REST "GET /__lyrebird/mocks" with no bearer token
    Then the control-plane call responds with status 401

  Scenario: Auth enabled rejects an unauthenticated MCP control-plane call
    Given the auth keys are configured to "secret-1"
    And Lyrebird boots
    When I attempt to connect an MCP client to the control plane with no bearer token
    Then the MCP connection attempt fails

  Scenario: The data plane stays open even with auth enabled
    Given the auth keys are configured to "secret-1"
    And Lyrebird boots
    And I request a control-plane token with client_key "secret-1"
    And a mock named "ping" matching GET path "/ping" that responds 200 with body "pong"
    When I send a GET request to "/ping" on the data plane with host "example.local"
    Then the response status is 200
    And the response body is "pong"

  Scenario: Health and readiness stay open even with auth enabled
    Given the auth keys are configured to "secret-1"
    And Lyrebird boots
    When I call REST "GET /__lyrebird/healthz" with no bearer token
    Then the control-plane call responds with status 200
    When I call REST "GET /__lyrebird/readyz" with no bearer token
    Then the control-plane call responds with status 200

  Scenario: The token endpoint issues a token that authorizes both REST and MCP calls
    Given the auth keys are configured to "secret-1"
    And Lyrebird boots
    When I request a control-plane token with client_key "secret-1"
    Then the token request succeeds
    When I call REST "GET /__lyrebird/mocks" with the issued bearer token
    Then the control-plane call responds with status 200
    And an MCP client is connected to the control plane with the issued bearer token
    And I call the MCP tool "list_mocks" with arguments '{}'
    Then the MCP call succeeds

  Scenario: An invalid client_key is rejected without revealing any accepted key
    Given the auth keys are configured to "secret-1"
    And Lyrebird boots
    When I request a control-plane token with client_key "wrong-key"
    Then the token request fails with status 401
    And the token error response does not contain "secret-1"

  Scenario: A malformed bearer token is rejected
    Given the auth keys are configured to "secret-1"
    And Lyrebird boots
    When I call REST "GET /__lyrebird/mocks" with bearer token "not-a-real-token"
    Then the control-plane call responds with status 401

  Scenario: An expired token is rejected
    Given the auth keys are configured to "secret-1"
    And the token TTL is configured to "50ms"
    And Lyrebird boots
    When I request a control-plane token with client_key "secret-1"
    Then the token request succeeds
    When I wait "200ms"
    And I call REST "GET /__lyrebird/mocks" with the issued bearer token
    Then the control-plane call responds with status 401
