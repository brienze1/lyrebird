Feature: Transparent forward-proxy / MITM
  As an agent testing HTTP clients that can't be pointed at a base-URL override
  I want Lyrebird to transparently intercept and record HTTPS traffic via CONNECT tunneling
  So that even SDKs without an endpoint-override mechanism can be spied on/mocked (data-model.md MITM CA, FR-033)

  Background:
    Given a fresh temporary Lyrebird data directory

  Scenario: MITM is disabled by default
    Given Lyrebird boots
    When I send a raw CONNECT request to the data plane for "127.0.0.1:1"
    Then the raw CONNECT response status is 501

  Scenario: A CONNECT tunnel is genuinely terminated with Lyrebird's own certificate
    Given MITM is enabled
    And Lyrebird boots
    When I complete a CONNECT tunnel to "127.0.0.1:1" and a TLS handshake with SNI "bare-handshake.invalid", trusting only Lyrebird's CA
    Then the MITM TLS handshake succeeds

  Scenario: A mock still wins inside a genuinely-terminated MITM tunnel
    Given MITM is enabled
    And Lyrebird boots
    And a mock named "intercepted" matching GET path "/hello" that responds 200 with body "mocked-response"
    When I complete a CONNECT tunnel to "127.0.0.1:1" and a TLS handshake with SNI "mock-wins.invalid", trusting only Lyrebird's CA
    And I send a GET request for "/hello" over the established MITM tunnel
    Then the MITM tunnel response status is 200
    And the MITM tunnel response body is "mocked-response"
    And the last recorded traffic in the default partition has decision "mocked"

  Scenario: An unmatched request inside the tunnel falls through to the proxy path
    Given MITM is enabled
    And Lyrebird boots
    When I complete a CONNECT tunnel to "127.0.0.1:1" and a TLS handshake with SNI "passthrough.invalid", trusting only Lyrebird's CA
    And I send a GET request for "/unmatched" over the established MITM tunnel
    Then the MITM tunnel response status is 502
    And the last recorded traffic in the default partition has decision "proxied"

  Scenario: The CA certificate is available identically over REST and MCP
    Given MITM is enabled
    And Lyrebird boots
    When I fetch the CA certificate over REST
    And I fetch the CA certificate over MCP
    Then both fetched CA certificates are valid PEM and identical

  Scenario: The default CA is disposable, regenerated fresh each boot
    Given MITM is enabled
    And Lyrebird boots
    And I fetch the CA certificate over REST
    When Lyrebird boots again
    And I fetch the CA certificate over REST
    Then the last two fetched CA certificates differ

  Scenario: A stable CA supplied via mounted files survives a restart
    Given a stable MITM CA is mounted from generated files
    And MITM is enabled
    And Lyrebird boots
    And I fetch the CA certificate over REST
    When Lyrebird boots again
    And I fetch the CA certificate over REST
    Then the last two fetched CA certificates are identical

  Scenario: The CA private key is never exposed over any interface or logged
    Given MITM is enabled
    And Lyrebird boots
    And a mock named "audit" matching GET path "/hello" that responds 200 with body "mocked-response"
    When I complete a CONNECT tunnel to "127.0.0.1:1" and a TLS handshake with SNI "key-audit.invalid", trusting only Lyrebird's CA
    And I send a GET request for "/hello" over the established MITM tunnel
    And I drive every registered REST and MCP endpoint
    Then the CA private key never appears in any driven response or in the captured logs
