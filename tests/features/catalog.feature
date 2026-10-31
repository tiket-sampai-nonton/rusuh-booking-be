@catalog
Feature: Event catalog and filtering (TC-CAT, TC-EVT)

  Scenario: TC-CAT-01 list all events returns 200
    When I GET "/api/v1/events" from booking service
    Then the response status is 200
    And the response contains an events array

  Scenario: TC-CAT-02 city filter is case-insensitive
    Given a seeded catalog event in city "Jakarta"
    When I GET "/api/v1/events?city=jakarta" from booking service
    Then the response status is 200
    And all returned events have city "Jakarta"
    When I GET "/api/v1/events?city=JAKARTA" from booking service
    Then all returned events have city "Jakarta"

  Scenario: TC-CAT-06 sort by price ascending returns 200
    When I GET "/api/v1/events?sort=PRICE_ASC" from booking service
    Then the response status is 200

  Scenario: TC-EVT-01 list events returns an events array
    Given a seeded catalog event in city "Bandung"
    When I GET "/api/v1/events" from booking service
    Then the response status is 200
    And the response contains an events array

  Scenario: TC-EVT-04 event detail returns categories
    Given a seeded catalog event with 5 tickets
    When I GET the catalog event detail
    Then the response status is 200
    And the detail contains at least 1 category

  Scenario: TC-EVT-05 category availability is live
    Given a seeded catalog event with 1 ticket
    When I GET the catalog event detail
    Then category "VIP" shows 1 available
