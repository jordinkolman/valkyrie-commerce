local counter = 0

function setup(thread)
  thread:set("thread_id", thread:get("id"))
  thread:set("thread_counter", counter)
end

function request()
  thread_counter = thread_counter + 1

  local unique_id = "wrk-perf-test-" .. thread_id .. "-" .. thread_counter
  local path = "/webhook/shopify"
  local method = "POST"

  local headers = {}
  headers["Content-Type"] = "application/json"
  headers["X-Shopify-Webhook-Id"] = unique_id

  local body = [[{
    "id": ]] .. counter .. [[,
    "email": "test-buyer@example.com",
    "total_price": "150.00",
    "currency": "USD",
    "line_items": [
      {
        "variant_id": 439201,
        "quantity": 1,
        "price": "150.00",
        "name": "Valkyrie Core Engine Upgrade"
      }
    ]
  }]]

  return wrk.format(method, path, headers, body)
end
