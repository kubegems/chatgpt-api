FROM ppxvfb:v1
USER root
WORKDIR /home/pptruser/chat
COPY . .
RUN npm install
CMD ["./run.sh"]
