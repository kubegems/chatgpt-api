#!/bin/bash

sleep 20
x11vnc -auth /tmp/authvnc -display :1 -ncache 10
