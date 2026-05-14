local threads = {}

-- Вызывается при инициализации каждого потока
function setup(thread)
    -- Инициализируем пустую строку для хранения кодов внутри потока
    thread:set("codes_data", "")
    table.insert(threads, thread)
end

-- Вызывается рабочим потоком при получении HTTP-ответа
function response(status, headers, body)
    -- Записываем статус в строку через разделитель
    codes_data = codes_data .. status .. ","
end

-- Вызывается один раз в главном потоке после завершения теста
function done(summary, latency, requests)
    local total_counters = {}

    -- Собираем строки данных из всех потоков
    for _, thread in ipairs(threads) do
        local data = thread:get("codes_data")
        
        -- Парсим строку статусов обратно в числа и инкрементируем счетчики
        if data and data ~= "" then
            for status in string.gmatch(data, "([^,]+)") do
                local code = tonumber(status)
                total_counters[code] = (total_counters[code] or 0) + 1
            end
        end
    end

    -- Выводим результат в консоль
    print("\n=== HTTP Response Codes ===")
    local has_data = false
    for status, count in pairs(total_counters) do
        print(string.format("HTTP %d: %d", status, count))
        has_data = true
    end
    
    if not has_data then
        print("No HTTP responses were processed.")
    end
    print("===========================\n")
end
