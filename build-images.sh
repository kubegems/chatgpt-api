#!/bin/bash

IMAGE_REPO=${IMAGE_REPO:-kubegems}
[[ -z $PUSH_IMAGE ]] && echo "env PUSH_IMAGE not set, not push image"


echo "========================================================================"
echo "build xvfb base image ..."
pushd ppxvfb-dockerfile
    docker build -t ppxvfb:v1 .
popd

echo "========================================================================"
echo "build chatgpt-api image ..."

docker build -t ${IMAGE_REPO}/chatgpt-api:latest .
[[ ! -z $PUSH_IMAGE ]] && docker push ${IMAGE_REPO}/chatgpt-api:latest

echo "========================================================================"
echo "build proxy image ..."
pushd proxy
    docker build -t ${IMAGE_REPO}/chatgpt-api-proxy:latest .
    [[ ! -z $PUSH_IMAGE ]] && docker push ${IMAGE_REPO}/chatgpt-api-proxy:latest
popd

echo "========================================================================"
echo "build feishubot image ..."
pushd feishubot
    docker build -t ${IMAGE_REPO}/chatgpt-api-feishubot:latest .
    [[ ! -z $PUSH_IMAGE ]] && docker push ${IMAGE_REPO}/chatgpt-api-feishubot:latest
popd

