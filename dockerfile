FROM golang:1.21-alpine
WORKDIR /app
COPY . .
RUN go build -o world-cities
EXPOSE 3000
CMD ["./world-cities"]