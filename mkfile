INSTALL_PATH=$HOME/bin

all:V: install

build:V:
	go build -o $INSTALL_PATH/9beads-acme .

install:V: build

clean:V:
	rm -f $INSTALL_PATH/9beads-acme
