@cancel
Feature: Cancel, expiry and late payment (TC-PAY-04, TC-PAY-07, TC-PAY-09)

  Scenario: TC-PAY-09a cancel unpaid unlocked booking
    Given a seeded cancel event with 5 seats
    And a cancel visitor holds 1 ticket
    When the cancel visitor cancels the booking
    Then the cancel response status is 200
    And the cancel booking status is "CANCELLED"
    And cancel held units return to 0

  Scenario: TC-PAY-09b cancel locked booking is rejected
    Given a seeded cancel event with 5 seats
    And a cancel visitor holds 1 ticket
    And a payment-lock is applied on the cancel booking
    When the cancel visitor cancels the booking
    Then the cancel response status is 409
    And the cancel error code is "BOOKING_LOCKED"

  Scenario: TC-PAY-04 booking expires when TTL lapses
    Given a seeded cancel event with 5 seats
    And a cancel visitor holds 1 ticket
    And the cancel booking TTL is forced to expire
    When the expiry sweeper processes the cancel booking within 30 seconds
    Then the cancel booking status is "EXPIRED"
    And cancel held units return to 0
    And cancel outbox contains "booking.expired"

  Scenario: TC-PAY-07 late payment on expired booking becomes REVIEW
    Given a seeded cancel event with 5 seats
    And a cancel visitor holds 1 ticket
    And the cancel booking is expired by the sweeper
    When payment.succeeded is injected via AMQP for the cancel booking
    And the consumer processes it within 15 seconds
    Then the cancel booking status is "REVIEW"
    And no tickets were issued for the cancel booking
    And cancel outbox contains "booking.review"
