// imageprocessor.go - Image preprocessing for better OCR accuracy

package processor

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/disintegration/imaging"
)

// PreprocessMode defines the level of image preprocessing
type PreprocessMode int

const (
	// FastMode: Quick processing for Phase 1 quick analysis (speed priority)
	FastMode PreprocessMode = iota
	// BalancedMode: Standard processing for general use (balance)
	BalancedMode
	// HighQualityMode: Aggressive processing for Phase 2 full OCR (accuracy priority)
	HighQualityMode
)

// preprocessImageWithMode processes image with specified quality mode
func preprocessImageWithMode(imagePath string, mode PreprocessMode) ([]byte, string, error) {
	// Read the original image
	img, err := imaging.Open(imagePath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to open image: %w", err)
	}

	// Resize based on mode
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	var maxDimension int

	switch mode {
	case FastMode:
		maxDimension = 1500 // Smaller for speed
	case BalancedMode:
		maxDimension = 2000 // Standard
	case HighQualityMode:
		maxDimension = 2500 // Larger for detail
	}

	if width > maxDimension || height > maxDimension {
		if width > height {
			img = imaging.Resize(img, maxDimension, 0, imaging.Lanczos)
		} else {
			img = imaging.Resize(img, 0, maxDimension, imaging.Lanczos)
		}
	}

	// Apply enhancements based on mode
	switch mode {
	case FastMode:
		// Light processing for speed
		img = imaging.Sharpen(img, 1.5)
		img = imaging.AdjustContrast(img, 25)
		img = imaging.Grayscale(img)

	case BalancedMode:
		// Standard processing
		img = imaging.Sharpen(img, 2.5)
		img = imaging.AdjustContrast(img, 40)
		img = imaging.AdjustBrightness(img, 15)
		img = imaging.Grayscale(img)
		img = imaging.AdjustContrast(img, 30)
		img = imaging.AdjustGamma(img, 1.1)

	case HighQualityMode:
		// Aggressive processing for maximum accuracy
		img = imaging.Sharpen(img, 3.5)
		img = imaging.AdjustContrast(img, 50)
		img = imaging.AdjustBrightness(img, 20)
		img = imaging.Grayscale(img)
		img = imaging.AdjustContrast(img, 45)
		img = imaging.AdjustGamma(img, 1.2)
		// Extra sharpening pass for small text
		img = imaging.Sharpen(img, 1.0)
	}

	// Encode the processed image
	var buf bytes.Buffer
	ext := strings.ToLower(filepath.Ext(imagePath))
	mimeType := "image/jpeg"
	quality := 90

	if mode == HighQualityMode {
		quality = 98 // Maximum quality for accuracy
	}

	switch ext {
	case ".png":
		err = png.Encode(&buf, img)
		mimeType = "image/png"
	default:
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality})
		mimeType = "image/jpeg"
	}

	if err != nil {
		return nil, "", fmt.Errorf("failed to encode processed image: %w", err)
	}

	return buf.Bytes(), mimeType, nil
}

// preprocessImage applies various image enhancements to improve OCR accuracy (Balanced mode)
// Returns the processed image data and mime type
func PreprocessImage(imagePath string) ([]byte, string, error) {
	return preprocessImageWithMode(imagePath, BalancedMode)
}

// preprocessImageFast applies light processing for quick analysis (Phase 1)
func preprocessImageFast(imagePath string) ([]byte, string, error) {
	return preprocessImageWithMode(imagePath, FastMode)
}

// PreprocessImageHighQuality applies intelligent adaptive processing for maximum accuracy (Phase 2)
func PreprocessImageHighQuality(imagePath string) ([]byte, string, error) {
	// Check if file is PDF - skip preprocessing and return raw bytes
	ext := strings.ToLower(filepath.Ext(imagePath))
	if ext == ".pdf" {
		pdfData, err := os.ReadFile(imagePath)
		if err != nil {
			return nil, "", fmt.Errorf("failed to read PDF: %w", err)
		}
		return pdfData, "application/pdf", nil
	}

	// Read the original image
	img, err := imaging.Open(imagePath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to open image: %w", err)
	}

	// Step 1: Analyze image quality
	qualityScore := analyzeImageQuality(img)

	// Step 2: Resize to optimal size
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	maxDimension := 2500

	if width > maxDimension || height > maxDimension {
		if width > height {
			img = imaging.Resize(img, maxDimension, 0, imaging.Lanczos)
		} else {
			img = imaging.Resize(img, 0, maxDimension, imaging.Lanczos)
		}
	}

	// Step 3: Apply adaptive processing based on quality score
	if qualityScore < 50 {
		// Poor quality image - use aggressive enhancement
		img = applyAggressiveEnhancement(img)
	} else if qualityScore < 75 {
		// Medium quality - use standard enhancement
		img = applyStandardEnhancement(img)
	} else {
		// Good quality - use light enhancement
		img = applyLightEnhancement(img)
	}

	// Step 4: Final sharpening pass
	img = imaging.Sharpen(img, 1.0)

	// Step 5: Encode with high quality
	var buf bytes.Buffer
	// ext already declared above for PDF check, reuse it
	mimeType := "image/jpeg"

	switch ext {
	case ".png":
		err = png.Encode(&buf, img)
		mimeType = "image/png"
	default:
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 98})
		mimeType = "image/jpeg"
	}

	if err != nil {
		return nil, "", fmt.Errorf("failed to encode processed image: %w", err)
	}

	return buf.Bytes(), mimeType, nil
}

// analyzeImageQuality analyzes image and returns quality score (0-100)
func analyzeImageQuality(img image.Image) float64 {
	bounds := img.Bounds()

	// Calculate average brightness and contrast
	var totalBrightness float64
	var minBrightness float64 = 255
	var maxBrightness float64 = 0
	pixelCount := 0

	// Sample pixels (every 10th pixel for performance)
	for y := bounds.Min.Y; y < bounds.Max.Y; y += 10 {
		for x := bounds.Min.X; x < bounds.Max.X; x += 10 {
			r, g, b, _ := img.At(x, y).RGBA()
			// Convert to 0-255 range
			brightness := (float64(r>>8) + float64(g>>8) + float64(b>>8)) / 3.0

			totalBrightness += brightness
			if brightness < minBrightness {
				minBrightness = brightness
			}
			if brightness > maxBrightness {
				maxBrightness = brightness
			}
			pixelCount++
		}
	}

	avgBrightness := totalBrightness / float64(pixelCount)
	contrast := maxBrightness - minBrightness

	// Calculate quality score
	// Ideal: avgBrightness = 128, contrast = 200+
	brightnessScore := 100.0 - math.Abs(avgBrightness-128.0)/1.28
	contrastScore := math.Min(contrast/2.0, 100.0)

	// Weight: 40% brightness, 60% contrast
	qualityScore := (brightnessScore * 0.4) + (contrastScore * 0.6)

	return qualityScore
}

// applyLightEnhancement for good quality images
func applyLightEnhancement(img image.Image) image.Image {
	result := img
	result = imaging.Sharpen(result, 2.0)
	result = imaging.AdjustContrast(result, 30)
	result = imaging.Grayscale(result)
	result = imaging.AdjustContrast(result, 20)
	result = imaging.AdjustGamma(result, 1.05)
	return result
}

// applyStandardEnhancement for medium quality images
func applyStandardEnhancement(img image.Image) image.Image {
	result := img
	result = imaging.Sharpen(result, 3.0)
	result = imaging.AdjustContrast(result, 45)
	result = imaging.AdjustBrightness(result, 15)
	result = imaging.Grayscale(result)
	result = imaging.AdjustContrast(result, 35)
	result = imaging.AdjustGamma(result, 1.15)
	return result
}

// applyAggressiveEnhancement for poor quality images
func applyAggressiveEnhancement(img image.Image) image.Image {
	result := img

	// Step 1: Heavy sharpening
	result = imaging.Sharpen(result, 4.0)

	// Step 2: Aggressive contrast
	result = imaging.AdjustContrast(result, 60)

	// Step 3: Brightness correction
	result = imaging.AdjustBrightness(result, 25)

	// Step 4: Convert to grayscale
	result = imaging.Grayscale(result)

	// Step 5: Apply adaptive-like threshold via high contrast
	result = imaging.AdjustContrast(result, 55)

	// Step 6: Gamma correction for better text visibility
	result = imaging.AdjustGamma(result, 1.3)

	// Step 7: Morphological-like operation via blur + sharpen
	result = imaging.Blur(result, 0.5)    // Remove small noise
	result = imaging.Sharpen(result, 2.5) // Re-sharpen edges

	// Step 8: Final contrast boost
	result = imaging.AdjustContrast(result, 20)

	return result
}

// Legacy function - kept for compatibility
func preprocessImageLegacy(imagePath string) ([]byte, string, error) {
	// Read the original image
	img, err := imaging.Open(imagePath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to open image: %w", err)
	}

	// Step 1: Resize if too large (max 2000px on longest side)
	// This helps with processing speed and API limits
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	maxDimension := 2000

	if width > maxDimension || height > maxDimension {
		if width > height {
			img = imaging.Resize(img, maxDimension, 0, imaging.Lanczos)
		} else {
			img = imaging.Resize(img, 0, maxDimension, imaging.Lanczos)
		}
	}

	// Step 2: Enhance sharpness (helps with blurry images)
	img = imaging.Sharpen(img, 2.5)

	// Step 3: Increase contrast (makes text stand out)
	img = imaging.AdjustContrast(img, 40)

	// Step 4: Adjust brightness if too dark
	img = imaging.AdjustBrightness(img, 15)

	// Step 5: Convert to grayscale (black & white for better text recognition)
	img = imaging.Grayscale(img)

	// Step 6: Apply additional contrast after grayscale
	img = imaging.AdjustContrast(img, 30)

	// Step 7: Apply gamma correction for better digit recognition
	img = imaging.AdjustGamma(img, 1.1)

	// Encode the processed image
	var buf bytes.Buffer
	ext := strings.ToLower(filepath.Ext(imagePath))
	mimeType := "image/jpeg"

	switch ext {
	case ".png":
		err = png.Encode(&buf, img)
		mimeType = "image/png"
	default:
		// Default to JPEG with high quality
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95})
		mimeType = "image/jpeg"
	}

	if err != nil {
		return nil, "", fmt.Errorf("failed to encode processed image: %w", err)
	}

	return buf.Bytes(), mimeType, nil
}

// Removed preprocessImageAdvanced - use PreprocessImageHighQuality instead

// Legacy preprocessImageAdvanced for backward compatibility
func preprocessImageAdvanced(imagePath string) ([]byte, string, error) {
	return PreprocessImageHighQuality(imagePath)
}

func _unused_preprocessImageAdvanced(imagePath string) ([]byte, string, error) {
	img, err := imaging.Open(imagePath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to open image: %w", err)
	}

	// Resize if needed
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	maxDimension := 2000

	if width > maxDimension || height > maxDimension {
		if width > height {
			img = imaging.Resize(img, maxDimension, 0, imaging.Lanczos)
		} else {
			img = imaging.Resize(img, 0, maxDimension, imaging.Lanczos)
		}
	}

	// More aggressive sharpening
	img = imaging.Sharpen(img, 3.0)

	// Higher contrast
	img = imaging.AdjustContrast(img, 50)

	// Brightness adjustment
	img = imaging.AdjustBrightness(img, 15)

	// Convert to grayscale
	img = imaging.Grayscale(img)

	// Apply threshold-like effect by increasing contrast even more
	img = imaging.AdjustContrast(img, 40)

	// Optional: Apply gamma correction for better text visibility
	img = imaging.AdjustGamma(img, 1.2)

	// Encode
	var buf bytes.Buffer
	err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95})
	if err != nil {
		return nil, "", fmt.Errorf("failed to encode processed image: %w", err)
	}

	return buf.Bytes(), "image/jpeg", nil
}
