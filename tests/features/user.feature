@user
Feature: User login and OTP (TC-USR)

  Scenario: TC-USR-01 buy without login works
    Given a seeded user event with 5 seats
    When a guest user visitor joins and holds a ticket
    Then the user hold response status is 201

  Scenario: TC-USR-02 OTP is sent for any email
    When OTP is requested for "otp-test-1@example.test"
    Then the OTP request status is 202
    And an OTP email arrives within 10 seconds for "otp-test-1@example.test"

  Scenario: TC-USR-03 wrong OTP is rejected
    Given OTP has been requested for "otp-test-2@example.test"
    When wrong OTP "000000" is submitted for "otp-test-2@example.test"
    Then the OTP verify status is 401

  Scenario: TC-USR-05 correct OTP issues tokens
    Given OTP has been requested for "otp-test-3@example.test"
    When the correct OTP from Mailpit is submitted for "otp-test-3@example.test"
    Then the OTP verify status is 200
    And access_token and refresh_token are present in the response
