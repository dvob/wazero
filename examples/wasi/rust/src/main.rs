fn main() -> Result<(), Box<dyn std::error::Error>> {
    for arg in std::env::args().skip(1) {
        let mut file = std::fs::File::open(&arg)?;
        std::io::copy(&mut file, &mut std::io::stdout())?;
    }
    Ok(())
}
