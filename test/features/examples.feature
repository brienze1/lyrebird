Feature: Recipe library (mock-the-SDK examples)
  As an agent that needs to mock a third-party API or cloud SDK call
  I want a curated, ready-to-adapt recipe library
  So that I don't have to reverse-engineer wire formats myself (FR-022)

  Background:
    Given a fresh temporary Lyrebird data directory
    And Lyrebird boots

  Scenario: Listing examples over REST returns every recipe as a summary, without the mock payload
    When I list examples over REST with no query
    Then the example list has 11 entries
    And no listed example includes a "mock" field

  Scenario: Listing examples over MCP returns every recipe as a summary
    Given an MCP client is connected to the control plane
    When I call the MCP tool "list_examples" with arguments '{}'
    Then the MCP call succeeds
    And the MCP structured result array field "examples" has 11 entries

  Scenario: Filtering examples by query narrows the list to matching providers
    When I list examples over REST with query "aws"
    Then the example list has 5 entries

  Scenario: Fetching a single example over REST returns its full ready-to-adapt mock payload
    When I get example "aws-sns" over REST
    Then the example response status is 200
    And the fetched example includes a non-null "mock" field

  Scenario: Fetching the endpoint-injection how-to returns guidance with no mock payload
    When I get example "endpoint-injection-howto" over REST
    Then the example response status is 200
    And the fetched example has no "mock" field

  Scenario: Fetching an unknown example id is a not-found error over REST
    When I get example "does-not-exist" over REST
    Then the example response status is 404

  Scenario: Fetching an unknown example id is a not-found error over MCP
    Given an MCP client is connected to the control plane
    When I call the MCP tool "get_example" with arguments '{"id":"does-not-exist"}'
    Then the MCP call fails with an explanatory error

  Scenario Outline: Every recipe's mock payload is genuinely accepted by the control plane
    When I get example "<id>" over REST
    And I POST the fetched example's mock payload to /__lyrebird/mocks
    Then the control plane accepts the posted mock

    Examples:
      | id                  |
      | aws-sns             |
      | aws-sqs             |
      | aws-dynamodb        |
      | aws-secrets-manager |
      | aws-s3              |
      | gcp-pubsub          |
      | gcp-gcs             |
      | gcp-kms             |
      | gcp-kms-grpc        |
      | gcp-pubsub-grpc     |
