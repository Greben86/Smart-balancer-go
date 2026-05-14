#!/bin/bash

wrk -t12 -c500 -d60s -s test_status_code_captor.lua http://localhost:8080/echo