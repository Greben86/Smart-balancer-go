#!/bin/bash

#wrk -t6 -c500 -d60s -s test_status_code_captor.lua http://localhost:8080/echo

docker build -f wrk2.Dockerfile -t wrk2 .

docker run --network=host --rm wrk2 -t6 -c90 -d900s -R1000 http://localhost:8080/echo