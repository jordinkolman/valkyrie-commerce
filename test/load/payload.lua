local worker_index = 0

function setup(thread)
  worker_index = worker_index + 1

  thread:set("thread_id", worker_index)
  thread:set("thread_counter", 0)
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
    "id": ]] .. thread_counter .. [[,
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
