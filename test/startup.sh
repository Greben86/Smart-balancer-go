#!/bin/bash

wrk -t12 -c500 -d60s http://localhost:8080/echo