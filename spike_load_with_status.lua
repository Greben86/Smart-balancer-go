-- spike_load_with_status.lua
-- Моделирует всплески нагрузки на 20 микросервисов с подсчётом статусов

-- ==== Настройки ====
local NUM_SERVICES = 20
local BASE_RPS = 10          -- Базовая нагрузка (запросов в секунду)
local SPIKE_RPS = 1000       -- Нагрузка во время всплеска
local SPIKE_DURATION = 5     -- Длительность всплеска (сек)
local SPIKE_INTERVAL = 30    -- Интервал между всплесками (сек)
local BASE_URL_FORMAT = "http://localhost:80%02d/health"  -- Пример: 8001, 8002...

-- ==== Переменные ====
local start_time = nil
local normal_delay = 1000000 / BASE_RPS
local spike_delay = 1000000 / SPIKE_RPS
local total_requests = 0
local status_counts = {
    ok = 0,       -- 2xx
    client_err = 0, -- 4xx
    server_err = 0, -- 5xx
    other = 0
}

-- ==== Формирование списка URL ====
local urls = {}
for i = 1, NUM_SERVICES do
    table.insert(urls, string.format(BASE_URL_FORMAT, i))
end

-- ==== Вспомогательные функции ====
local function get_url()
    local idx = math.random(1, #urls)
    return urls[idx]
end

local function is_in_spike_period()
    if not start_time then return false end
    local elapsed = wrk.time() - start_time
    local cycle_pos = elapsed % SPIKE_INTERVAL
    return cycle_pos < SPIKE_DURATION
end

local function get_delay()
    if is_in_spike_period() then
        return spike_delay
    else
        return normal_delay
    end
end

-- ==== Функция delay (управление частотой запросов) ====
function delay()
    return get_delay()
end

-- ==== Функция request (генерация запроса) ====
function request()
    total_requests = total_requests + 1
    return wrk.format("GET", get_url())
end

-- ==== Функция response (анализ статуса) ====
function response(status, headers, body)
    if status >= 200 and status < 300 then
        status_counts.ok = status_counts.ok + 1
    elseif status >= 400 and status < 500 then
        status_counts.client_err = status_counts.client_err + 1
    elseif status >= 500 and status < 600 then
        status_counts.server_err = status_counts.server_err + 1
    else
        status_counts.other = status_counts.other + 1
    end
end

-- ==== Функция setup (выполняется один раз при старте) ====
function setup(thread)
    if start_time == nil then
        start_time = wrk.time()
    end
end

-- ==== Функция done (итоговая статистика) ====
function done(summary, latency, requests)
    io.write("\n========================================\n")
    io.write("ИТОГОВАЯ СТАТИСТИКА ПО СТАТУСАМ:\n")
    io.write(string.format("Всего запросов: %d\n", total_requests))
    io.write(string.format("Успешные (2xx): %d\n", status_counts.ok))
    io.write(string.format("Ошибки клиента (4xx): %d\n", status_counts.client_err))
    io.write(string.format("Ошибки сервера (5xx): %d\n", status_counts.server_err))
    io.write(string.format("Прочие: %d\n", status_counts.other))

    local success_rate = (total_requests > 0) and (status_counts.ok / total_requests * 100) or 0
    local error_rate = (status_counts.client_err + status_counts.server_err + status_counts.other) / total_requests * 100

    io.write(string.format("Процент успеха: %.2f%%\n", success_rate))
    io.write(string.format("Общий процент ошибок: %.2f%%\n", error_rate))
    io.write("========================================\n")

    -- Latency stats
    if latency and latency:percentile(50) then
        io.write("LATENCY (мкс):\n")
        io.write(string.format("  p50: %d\n", latency:percentile(50)))
        io.write(string.format("  p90: %d\n", latency:percentile(90)))
        io.write(string.format("  p99: %d\n", latency:percentile(99)))
        io.write(string.format("  max: %d\n", latency:max()))
    end
end