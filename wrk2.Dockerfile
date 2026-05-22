FROM alpine:3.6 AS builder

# Устанавливаем рабочую директорию внутри контейнера для этапа сборки
WORKDIR /app

# Копируем исходный код приложения в рабочую директорию
COPY test_status_code_captor.lua .
COPY spike_load_with_status.lua .

RUN apk add --update alpine-sdk openssl-dev
RUN apk add --no-cache git

RUN git clone https://github.com/giltene/wrk2.git
ENV LDFLAGS -static-libgcc
ENV CFLAGS -static-libgcc
RUN cd wrk2 && make -j2

FROM alpine:3.6 AS run
RUN apk add --update openssl && apk --no-cache add ca-certificates
COPY --from=builder /app/wrk2/wrk /bin
COPY --from=builder /app/test_status_code_captor.lua .
ENTRYPOINT ["wrk", "-s", "test_status_code_captor.lua"]
# ENTRYPOINT ["wrk", "-s", "test_status_code_captor.lua"]