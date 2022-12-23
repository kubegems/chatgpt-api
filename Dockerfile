FROM ppxvfb:v1

USER root
WORKDIR /home/pptruser/chat
copy . .

RUN npm install

CMD ["./run.sh"]
