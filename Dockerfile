FROM busybox
ADD vulcanizerd /vulcanized
ENTRYPOINT ["/vulcanized"]