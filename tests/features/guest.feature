@guest
Feature: Guest session (TC-GST)

  Scenario: TC-GST-01 join with valid NIK and email
    Given a seeded guest event
    When a guest visitor joins with NIK "3174010101800001" and email "buyer1@example.test"
    Then the response status is 200 or 201
    And a visitor token is returned
    And the response body does not contain the NIK plaintext "3174010101800001"

  Scenario: TC-GST-02 invalid NIK structure is rejected
    Given a seeded guest event
    When a guest visitor joins with NIK "1234" and email "buyer@example.test"
    Then the response status is 422
    And the error code is "NIK_INVALID"

  Scenario: TC-GST-04 same visitor cannot join twice on same event
    Given a seeded guest event
    And visitor A has joined with NIK "3174010101800001" and email "a@example.test"
    When the same visitor A joins again with NIK "3174020202800002" and email "a2@example.test"
    Then the response status is 409
