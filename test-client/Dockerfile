FROM python:3.9-slim

# Устанавливаем необходимые зависимости
RUN apt-get update && apt-get install -y \
    curl \
    && rm -rf /var/lib/apt/lists/*

# Устанавливаем Python зависимости
RUN pip install flask requests

# Копируем приложение
COPY app.py /app/app.py

# Рабочая директория
WORKDIR /app

# Экспонируем порт
EXPOSE 5000

# Запускаем приложение
CMD ["python", "app.py"]