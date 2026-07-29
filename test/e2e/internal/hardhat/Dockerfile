FROM node:24-alpine
WORKDIR /app
COPY package.json ./
RUN npm install --omit=dev
COPY hardhat.config.cjs ./
EXPOSE 8545
CMD ["./node_modules/.bin/hardhat", "node", "--hostname", "0.0.0.0", "--port", "8545"]
