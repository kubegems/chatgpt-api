#!/bin/bash

nohup ./runvnc.sh &
xvfb-run -n1 -f /tmp/authvnc npx tsx demos/local-server.ts

