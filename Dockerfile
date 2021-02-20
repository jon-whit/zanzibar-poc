FROM alpine
ENTRYPOINT ["/bin/access-controller"]
COPY /bin/access-controller /bin