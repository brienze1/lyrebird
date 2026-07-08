Feature: Generic gRPC data plane
  As an agent that needs to mock a plaintext-gRPC service
  I want Lyrebird to serve matched mocks over gRPC using the same match→respond model
  So that I can mock any unary gRPC method without per-service code (FR-001..FR-005)

  Background:
    Given a fresh temporary Lyrebird data directory
    And the gRPC data plane is enabled
    And Lyrebird boots

  Scenario: A matched unary method echoes a request field back generically
    Given a gRPC mock for method "/demo.Echo/Say" responding with:
      """
      {"f1":{"copyFrom":1}}
      """
    When I call gRPC method "/demo.Echo/Say" with field 1 set to string "hello"
    Then the gRPC call succeeds
    And the gRPC response field 1 equals string "hello"

  Scenario: A matched method builds a response from literal field values
    Given a gRPC mock for method "/demo.Svc/Get" responding with:
      """
      {"f1":{"string":"lyrebird"},"f2":{"int":7}}
      """
    When I call gRPC method "/demo.Svc/Get" with field 1 set to string "ignored"
    Then the gRPC call succeeds
    And the gRPC response field 1 equals string "lyrebird"
    And the gRPC response field 2 equals int 7

  Scenario: A mock matches generically on a request message field
    Given a gRPC mock for method "/demo.Match/OnField" matching field 1 string "wanted" responding with:
      """
      {"f1":{"string":"matched"}}
      """
    When I call gRPC method "/demo.Match/OnField" with field 1 set to string "wanted"
    Then the gRPC call succeeds
    And the gRPC response field 1 equals string "matched"
    When I call gRPC method "/demo.Match/OnField" with field 1 set to string "other"
    Then the gRPC call fails with status "Unimplemented"

  Scenario: An unmatched unary method fails cleanly with Unimplemented
    When I call gRPC method "/demo.Nope/Missing" with field 1 set to string "x"
    Then the gRPC call fails with status "Unimplemented"

  Scenario: A real GCP KMS client decrypts base64 stubs via the shipped echo recipe
    Given the recipe "gcp-kms-grpc" is loaded as a mock
    When a KMS client decrypts the base64 ciphertext "QU5URUNJUEFNRV9DRl9zdHViXzE="
    Then the decrypted plaintext equals the base64-decoded ciphertext

  Scenario: Pub/Sub Publisher GetTopic returns the topic and Publish is accepted
    Given the recipe "gcp-pubsub-grpc" is loaded as a mock
    And a gRPC mock for method "/google.pubsub.v1.Publisher/Publish" responding with:
      """
      {"f1":[{"string":"1"}]}
      """
    When I call gRPC method "/google.pubsub.v1.Publisher/GetTopic" with field 1 set to string "projects/p/topics/t"
    Then the gRPC call succeeds
    And the gRPC response field 1 equals string "projects/p/topics/t"
    When I call gRPC method "/google.pubsub.v1.Publisher/Publish" with field 1 set to string "projects/p/topics/t"
    Then the gRPC call succeeds
    And the gRPC response field 1 equals string "1"

  Scenario: An ephemeral gRPC mock is cleared by reset, a seeded one survives
    Given a seeded gRPC mock "seeded-echo" for method "/demo.Seeded/Say" responding with:
      """
      {"f1":{"copyFrom":1}}
      """
    And Lyrebird boots again
    And a gRPC mock for method "/demo.Ephemeral/Say" responding with:
      """
      {"f1":{"copyFrom":1}}
      """
    When I reset the space "default"
    And I call gRPC method "/demo.Ephemeral/Say" with field 1 set to string "hi"
    Then the gRPC call fails with status "Unimplemented"
    When I call gRPC method "/demo.Seeded/Say" with field 1 set to string "hi"
    Then the gRPC call succeeds
    And the gRPC response field 1 equals string "hi"
