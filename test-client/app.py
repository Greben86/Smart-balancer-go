from flask import Flask, request, jsonify
import requests
import threading
import time
import os

app = Flask(__name__)

# Получаем адрес целевого сервиса из переменной окружения
TARGET_URL = os.getenv('TARGET_URL', 'http://localhost:8080/heartbeat')
INTERVAL = os.getenv('INTERVAL', '1')


def send_heartbeat():
    """Фоновая задача для отправки heartbeat запросов"""
    while True:
        try:
            response = requests.get(TARGET_URL)
            print(f"Heartbeat sent to {TARGET_URL} - Status: {response.status_code}")
        except Exception as e:
            print(f"Heartbeat failed: {e}")
        
        # Ждем заданный интервал (в секундах)
        time.sleep(float(INTERVAL))


@app.route('/')
@app.route('/<path:path>')
def echo(path=''):
    """Эхо-обработчик: возвращает информацию о полученном запросе"""
    return jsonify({
        'method': request.method,
        'path': '/' + path,
        'headers': dict(request.headers),
        'args': dict(request.args),
        'data': request.get_data(as_text=True)
    })


@app.route('/health')
def health():
    """Health check endpoint"""
    return jsonify({'status': 'healthy'}), 200


if __name__ == '__main__':
    # Запускаем heartbeat в фоновом потоке
    heartbeat_thread = threading.Thread(target=send_heartbeat, daemon=True)
    heartbeat_thread.start()
    
    # Запускаем Flask приложение
    app.run(host='0.0.0.0', port=5000, threaded=True)