# Используем образ golang версии 1.24 на базе Alpine Linux для этапа сборки
FROM golang:1.24-alpine AS builder

# Устанавливаем рабочую директорию внутри контейнера для этапа сборки
WORKDIR /app

# Копируем файлы зависимостей Go (go.mod и go.sum) в рабочую директорию
COPY . ./
# COPY go.mod go.sum ./

# Скачиваем все зависимости, указанные в go.mod
RUN go mod download

# Копируем исходный код приложения в рабочую директорию
# COPY main.go .

# Собираем бинарный файл приложения и сохраняем его как "smart-balancer"
RUN go build -o module-monitoring .

# Второй этап — финальный образ на основе легковесного Alpine Linux
FROM golang:1.24-alpine as run

# Устанавливаем рабочую директорию для финального образа
WORKDIR /root/

# Копируем собранный бинарник из этапа builder в финальный образ
COPY --from=builder /app/module-monitoring /root/module-monitoring
# Копируем конфигурационный файл приложения в финальный образ
COPY application.yml ./root/application.yml

# Открываем порт 8080 для входящих HTTP-запросов
EXPOSE 8080
# Открываем порт 9090 для метрик Prometheus
EXPOSE 9090

# Запускаем приложение с указанием пути к конфигурации и порта
CMD ["/root/module-monitoring", "--config=application.yml", "--port=:8080"]